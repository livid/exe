//go:build !windows

package server

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
)

type unixShell struct {
	f     *os.File
	cmd   *exec.Cmd
	mu    sync.Mutex
	tty   string // the pty's tmux client tty once known (clientTTY)
	wheel int    // the agent's lines per wheel report (hostAgent.wheel)
	owed  int    // wheel lines not yet a whole report (Scroll)
}

func (s *unixShell) Read(p []byte) (int, error)  { return s.f.Read(p) }
func (s *unixShell) Write(p []byte) (int, error) { return s.f.Write(p) }

func (s *unixShell) Resize(cols, rows int) {
	pty.Setsize(s.f, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
}

// Close hangs the session up the way a terminal emulator's close box does,
// taking the window's foreground job with it. SIGHUP goes to the shell (the
// pty's session leader) while the master is still open, so when the shell
// exits the kernel delivers SIGHUP to the whole foreground process group as
// a controlling terminal still exists: a foreground `sleep` dies with its
// window, a nohup'd job lives on, as in any terminal. Only then is the
// master closed. The old code closed the master first and SIGKILLed just
// the shell, which tore down that terminal before the exit and left the
// window's children orphaned to init. SIGKILL to the process group is now
// only the fallback for a shell that ignores the hangup. (An agent
// window's `cmd` is a tmux client, and hanging it up simply detaches,
// leaving the persistent session for the icon to return to.)
func (s *unixShell) Close() error {
	s.Scroll(0) // a view scrolled back does not outlive its window: the next opens live
	s.cmd.Process.Signal(syscall.SIGHUP)
	done := make(chan struct{})
	go func() { s.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		syscall.Kill(-s.cmd.Process.Pid, syscall.SIGKILL) // pty.Start put it in its own group
		s.cmd.Process.Kill()
		<-done
	}
	return s.f.Close()
}

// startAgent runs an agent's CLI (Claude Code, Codex) on a pty in the
// project dir, and names the session the window opens on. With tmux on
// the host the CLI lives inside a persistent session of its own
// ("exe-claude", "exe-codex"): closing the window only detaches, and the
// desktop icon returns to the running conversation — to the session the
// window showed last when the column has moved it (lastAgentSession),
// attached directly so nothing is shown first and then switched away
// from, whichever browser reopens it. Without a session shown yet the
// icon's own is attached, created when it does not exist (-A). Any
// other client of the session stays attached: a phone's window beside
// the desktop's, or the same window's reconnect while the daemon has
// yet to notice its old link died. The pane still follows the newest
// window — tmux sizes a window to the client that most recently
// attached, typed or resized (window-size latest, its default), which a
// stale client never does again, and the link's pulse
// (handleHostTerminal) retires it within the minute. Attaching with -d
// kicked the others instead, and a kicked window read its own end in
// that ("session ended") and stayed dead until the page was reloaded.
// When none of the agent's sessions runs but a restart of the host
// stopped some, the one shown last is continued and attached instead
// (wakeLastStopped, agentrestart.go): the window lands where it was.
// Without tmux each window is a fresh CLI run. The CLI is launched
// with the arguments that point its hooks at the session's status file
// (agentCommand) — the session's first launch decides, as -A attaching
// ignores the command line — and that file is cleared for a fresh
// conversation, so the window never opens on the last one's figures.
func (s *Server) startAgent(a hostAgent, cols, rows int) (agentShell, string, error) {
	bin := agentPath(a)
	if bin == "" {
		return nil, "", fmt.Errorf("%s is not installed on this host", a.title)
	}
	dir := s.agentProjectDir()
	file := s.agentStatusFile(a, a.session)
	args, line := agentCommand(a, bin, file)
	if args != nil {
		os.MkdirAll(filepath.Dir(file), 0o755)
	}
	session := a.session
	cmd := exec.Command(bin, args...)
	if has := tmuxCmd("has-session", "-t", "="+a.session); has != nil {
		last := s.lastAgentSession(a)
		if last == "" {
			last = s.wakeLastStopped(a)
		}
		if last != "" {
			session = last
			cmd = tmuxCmd("attach-session", "-t", "="+last)
		} else {
			if has.Run() != nil {
				os.Remove(file)
				os.Remove(stateFileOf(file))
			}
			cmd = tmuxCmd("new-session", "-A", "-s", a.session, "-c", dir, line)
		}
	} else {
		os.Remove(file)
		os.Remove(stateFileOf(file))
	}
	cmd.Dir = dir
	cmd.Env = cliEnv(bin)
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		return nil, "", err
	}
	return &unixShell{f: f, cmd: cmd, wheel: a.wheel}, session, nil
}

// agentCommand is the CLI's launch inside a tmux session: the arguments
// that point its hooks at file — Claude Code's status line and state
// hooks, Codex's notify command and terminal title (agentStatusArgs,
// none for a CLI without any) — and the command line the session runs.
// That goes through env so the CLI's own directory is on PATH inside the
// session too: a tmux server's environment is fixed when it starts (by
// the first agent opened), and an npm shim like codex under nvm needs
// the node beside it.
func agentCommand(a hostAgent, bin, file string) (args []string, line string) {
	args = agentStatusArgs(a, file, agentSettingsPath(a))
	if a.notify && codexHasNoDaemon(bin) {
		args = append([]string{"--no-daemon"}, args...)
	}
	line = "env " + shQuote("PATH="+cliPATH(bin)) + " " + shQuote(bin)
	for _, arg := range args {
		line += " " + shQuote(arg)
	}
	return args, line
}

// Codex's per-session config overrides require its embedded server.
// Choose that mode explicitly to avoid the shared-server warning, but
// leave older CLIs that do not know --no-daemon on their existing path.
func codexHasNoDaemon(bin string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "--help")
	cmd.Env = cliEnv(bin)
	cmd.WaitDelay = 100 * time.Millisecond
	out, err := cmd.Output()
	return err == nil && slices.Contains(strings.Fields(string(out)), "--no-daemon")
}

// newAgentSession starts a detached tmux session of the agent's under
// name — the window's session column adding a conversation — with the
// CLI launched as startAgent launches the icon's own and a status file
// of the session's own, cleared first. A window moves to it with Switch.
// extra are further CLI arguments (agentLaunchArgs: a resume, a session
// id), quoted onto the same command line.
func (s *Server) newAgentSession(a hostAgent, name string, extra ...string) error {
	return s.newAgentSessionIn(a, name, "", extra...)
}

// newAgentSessionIn is newAgentSession in a folder of the caller's —
// a resumed thread's own (agentColumn.resume) — the project folder
// when dir is "".
func (s *Server) newAgentSessionIn(a hostAgent, name, dir string, extra ...string) error {
	return s.newAgentTaskSession(a, name, dir, "", extra...)
}

// newAgentTaskSession is newAgentSessionIn with a first message the CLI
// sends as it starts (task, "" for none): the agents API's prompt, a
// Projects issue sent to the agent. It does not ride on the command line
// with the rest: tmux takes a command of at most 16 KiB from its client
// ("command too long"), fewer characters still once each quote is
// quoted, and a long issue is more. So it waits in a file of its own
// beside the status files, and the session's shell reads it into one
// argument after "--" — a task that starts with a dash is no option —
// and removes it before the CLI starts. The file is named apart from
// the session, so a launch that loses a race for the name removes only
// its own.
func (s *Server) newAgentTaskSession(a hostAgent, name, dir, task string, extra ...string) (err error) {
	if err := agentTaskOK(task); err != nil {
		return err
	}
	bin := agentPath(a)
	if bin == "" {
		return fmt.Errorf("%s is not installed on this host", a.title)
	}
	if dir == "" {
		dir = s.agentProjectDir()
	}
	file := s.agentStatusFile(a, name)
	args, line := agentCommand(a, bin, file)
	if args != nil || task != "" {
		os.MkdirAll(filepath.Dir(file), 0o755)
	}
	for _, e := range extra {
		line += " " + shQuote(e)
	}
	if task != "" {
		f, ferr := os.CreateTemp(filepath.Dir(file), name+"-*.task")
		if ferr != nil {
			return ferr
		}
		tf := f.Name()
		defer func() {
			if err != nil {
				os.Remove(tf)
			}
		}()
		_, werr := f.WriteString(task)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return werr
		}
		line += ` -- "$(cat ` + shQuote(tf) + `; rm -f ` + shQuote(tf) + `)"`
	}
	os.Remove(file)
	os.Remove(stateFileOf(file))
	cmd := tmuxCmd("new-session", "-d", "-s", name, "-c", dir, line)
	if cmd == nil {
		return fmt.Errorf("a second session needs tmux on this host")
	}
	cmd.Dir = dir
	cmd.Env = cliEnv(bin)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux new-session: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// tmuxClient finds the pty's tmux client — the one whose pid is the
// command on the pty — and the session it shows; "" for a pty that is no
// tmux client (a Terminal window, or an agent run without tmux). Colons
// separate the fields: neither a tty path nor a session name holds one.
func (s *unixShell) tmuxClient() (tty, session string) {
	cmd := tmuxCmd("list-clients", "-F", "#{client_pid}:#{client_tty}:#{client_session}")
	if cmd == nil {
		return "", ""
	}
	out, err := cmd.Output()
	if err != nil {
		return "", ""
	}
	pid := strconv.Itoa(s.cmd.Process.Pid)
	for _, line := range strings.Split(string(out), "\n") {
		if f := strings.Split(line, ":"); len(f) == 3 && f[0] == pid {
			return f[1], f[2]
		}
	}
	return "", ""
}

func (s *unixShell) Current() string {
	_, session := s.tmuxClient()
	return session
}

// clientTTY is the pty's tmux client tty, looked up once: the client
// keeps its tty for the life of the pty while the session it shows
// changes, and every tmux command below names it as its target.
func (s *unixShell) clientTTY() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tty == "" {
		s.tty, _ = s.tmuxClient()
	}
	return s.tty
}

// Scroll moves the window's view through the pane's tmux history — the
// browser's wheel while nothing in the pane takes the mouse. The browser
// terminal cannot do it itself: tmux draws in its alternate screen, so
// xterm.js keeps no scrollback there and turns the wheel into arrow
// keys, which reach the CLI as typed (Codex's composer walked its prompt
// history on every notch; Claude Code tracks the mouse and takes the
// wheel itself, so it never comes here). lines > 0 scrolls back that
// many lines, entering tmux's copy mode — with -e, scrolling down to
// the bottom again leaves it; lines < 0 scrolls forward; 0 leaves copy
// mode, which the browser sends ahead of any key typed while scrolled
// back, so the key reaches the CLI as it would in any terminal. A pane
// in its own alternate screen (vim, less) has no history: the wheel
// becomes arrow keys there, as xterm.js does on its own. Codex draws
// there too since 0.160, with a transcript of its own, and asks the
// terminal for just those arrow keys (?1007h) — which its composer
// takes as a walk through past prompts. What scrolls its transcript is
// a mouse wheel report, three lines each, though it never asks for the
// mouse: so an agent with a wheel of its own (hostAgent.wheel) gets
// reports, aimed a third of the way down the pane — Codex heeds them
// over the transcript only, not over the prompt pinned to the top line
// or the composer — and lines short of a whole report are owed to the
// next. Scrolled back there, a typed key leaves the view where it is:
// that is Codex's to decide. Every command names the client's tty as
// its target: an if-shell's inner commands without one land on tmux's
// idea of the current session — someone else's window.
func (s *unixShell) Scroll(lines int) error {
	tty := s.clientTTY()
	if tty == "" {
		return nil // no tmux client: the browser terminal scrolls itself
	}
	out, err := tmuxCmd("display-message", "-p", "-t", tty, "#{alternate_on} #{pane_in_mode} #{pane_width} #{pane_height}").Output()
	if err != nil {
		return fmt.Errorf("tmux display-message: %w", err)
	}
	f := strings.Fields(string(out))
	alt := len(f) == 4 && f[0] == "1"
	mode := len(f) == 4 && f[1] == "1"
	n := lines
	if n < 0 {
		n = -n
	}
	count := strconv.Itoa(n)
	var cmd *exec.Cmd
	switch {
	case lines == 0:
		s.mu.Lock()
		s.owed = 0
		s.mu.Unlock()
		if !mode {
			return nil
		}
		cmd = tmuxCmd("send-keys", "-t", tty, "-X", "cancel")
	case alt && !mode && s.wheel > 0:
		s.mu.Lock()
		s.owed += lines
		reports := s.owed / s.wheel
		s.owed -= reports * s.wheel
		s.mu.Unlock()
		if reports == 0 {
			return nil
		}
		button := 64 // wheel up, in SGR: back through the transcript
		if reports < 0 {
			button, reports = 65, -reports
		}
		cols, _ := strconv.Atoi(f[2])
		rows, _ := strconv.Atoi(f[3])
		report := fmt.Sprintf("\x1b[<%d;%d;%dM", button, max(cols/2, 1), max(rows/3, 2))
		cmd = tmuxCmd("send-keys", "-t", tty, "-l", "-N", strconv.Itoa(reports), report)
	case alt && !mode:
		key := "Up"
		if lines < 0 {
			key = "Down"
		}
		cmd = tmuxCmd("send-keys", "-t", tty, "-N", count, key)
	case lines > 0 && mode:
		cmd = tmuxCmd("send-keys", "-t", tty, "-X", "-N", count, "scroll-up")
	case lines > 0:
		cmd = tmuxCmd("copy-mode", "-e", "-t", tty, ";", "send-keys", "-t", tty, "-X", "-N", count, "scroll-up")
	case mode:
		cmd = tmuxCmd("send-keys", "-t", tty, "-X", "-N", count, "scroll-down")
	default:
		return nil // forward with nothing scrolled back: already live
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// Switch moves the pty's tmux client to another session; the pty keeps
// its size and tmux lays the session out for it.
func (s *unixShell) Switch(session string) error {
	tty := s.clientTTY()
	if tty == "" {
		return fmt.Errorf("this window is not a tmux client")
	}
	out, err := tmuxCmd("switch-client", "-c", tty, "-t", "="+session).CombinedOutput()
	if err != nil {
		return fmt.Errorf("switch-client: %s", strings.TrimSpace(string(out)))
	}
	s.Scroll(0) // the session comes up live, not where a wheel once left it
	return nil
}

// loginShell is the user's shell: $SHELL, else the system's default.
func loginShell() string {
	if shell := os.Getenv("SHELL"); shell != "" {
		return shell
	}
	if runtime.GOOS == "darwin" {
		return "/bin/zsh"
	}
	return "/bin/sh"
}

// newTermSession starts a Terminal session for a new window, detached,
// under the lowest number free: a login shell in the home folder, as a
// one-off Terminal runs (startHostShell), with TMUX unset in it and
// tmux's own status line and prefix key off (termsessions.go). The
// window attaches with startTermClient and sizes the session to itself.
func (s *Server) newTermSession() (termSession, error) {
	s.termMu.Lock()
	defer s.termMu.Unlock()
	used := map[int]bool{}
	for _, t := range s.termSessions() {
		used[t.Number] = true
	}
	n := 1
	for used[n] {
		n++
	}
	name := termSessionName(n)
	args := []string{"new-session", "-d", "-s", name, "-x", "80", "-y", "24"}
	home, err := os.UserHomeDir()
	if err == nil {
		args = append(args, "-c", home)
	}
	args = append(args, "exec env -u TMUX -u TMUX_PANE "+shQuote(loginShell())+" -l",
		";", "set-option", "-t", name, "status", "off",
		";", "set-option", "-t", name, "prefix", "None",
		";", "set-option", "-t", name, "prefix2", "None")
	cmd := tmuxCmd(args...)
	if cmd == nil {
		return termSession{}, errNoTmux
	}
	cmd.Dir = home
	cmd.Env = termEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		return termSession{}, fmt.Errorf("tmux new-session: %s", strings.TrimSpace(string(out)))
	}
	delete(s.termClosed, name) // a number used before starts over
	return termSession{Name: name, Number: n, Created: time.Now().Unix()}, nil
}

// startTermClient attaches a window's tmux client to a Terminal session.
// Closing it (unixShell.Close) only detaches: the shell lives on.
func startTermClient(name string, cols, rows int) (hostShell, error) {
	cmd := tmuxCmd("attach-session", "-t", "="+name)
	if cmd == nil {
		return nil, errNoTmux
	}
	cmd.Env = termEnv()
	if home, err := os.UserHomeDir(); err == nil {
		cmd.Dir = home
	}
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		return nil, err
	}
	return &unixShell{f: f, cmd: cmd}, nil
}

// startHostShell starts the user's login shell on a pty; a non-empty command
// runs in it instead of a prompt (-l so the profile's PATH applies — the
// daemon's own is often slim), and the session ends when it exits.
func startHostShell(command string, cols, rows int) (hostShell, error) {
	shell := loginShell()
	cmd := exec.Command(shell, "-l")
	if command != "" {
		cmd = exec.Command(shell, "-l", "-c", command)
	}
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	if home, err := os.UserHomeDir(); err == nil {
		cmd.Dir = home
	}
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		return nil, err
	}
	return &unixShell{f: f, cmd: cmd}, nil
}
