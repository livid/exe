//go:build !windows

package server

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
)

type unixShell struct {
	f   *os.File
	cmd *exec.Cmd
	mu  sync.Mutex
	tty string // the pty's tmux client tty once known (clientTTY)
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
// from, whichever browser reopens it; -d, like -D below, kicks any stale
// client so the pty size follows the newest window. Without a session
// shown yet the icon's own is attached, created when it does not exist
// (-A). Without tmux each window is a fresh CLI run. The CLI is launched
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
		if last := s.lastAgentSession(a); last != "" {
			session = last
			cmd = tmuxCmd("attach-session", "-d", "-t", "="+last)
		} else {
			if has.Run() != nil {
				os.Remove(file)
				os.Remove(stateFileOf(file))
			}
			cmd = tmuxCmd("new-session", "-A", "-D", "-s", a.session, "-c", dir, line)
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
	return &unixShell{f: f, cmd: cmd}, session, nil
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
	line = "env " + shQuote("PATH="+cliPATH(bin)) + " " + shQuote(bin)
	for _, arg := range args {
		line += " " + shQuote(arg)
	}
	return args, line
}

// newAgentSession starts a detached tmux session of the agent's under
// name — the window's session column adding a conversation — with the
// CLI launched as startAgent launches the icon's own and a status file
// of the session's own, cleared first. A window moves to it with Switch.
// extra are further CLI arguments (agentLaunchArgs: a resume, a session
// id, the first message), quoted onto the same command line.
func (s *Server) newAgentSession(a hostAgent, name string, extra ...string) error {
	bin := agentPath(a)
	if bin == "" {
		return fmt.Errorf("%s is not installed on this host", a.title)
	}
	dir := s.agentProjectDir()
	file := s.agentStatusFile(a, name)
	args, line := agentCommand(a, bin, file)
	if args != nil {
		os.MkdirAll(filepath.Dir(file), 0o755)
	}
	for _, e := range extra {
		line += " " + shQuote(e)
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
// becomes arrow keys there, as xterm.js does on its own. Every command
// names the client's tty as its target: an if-shell's inner commands
// without one land on tmux's idea of the current session — someone
// else's window.
func (s *unixShell) Scroll(lines int) error {
	tty := s.clientTTY()
	if tty == "" {
		return nil // no tmux client: the browser terminal scrolls itself
	}
	out, err := tmuxCmd("display-message", "-p", "-t", tty, "#{alternate_on} #{pane_in_mode}").Output()
	if err != nil {
		return fmt.Errorf("tmux display-message: %w", err)
	}
	f := strings.Fields(string(out))
	alt := len(f) == 2 && f[0] == "1"
	mode := len(f) == 2 && f[1] == "1"
	n := lines
	if n < 0 {
		n = -n
	}
	count := strconv.Itoa(n)
	var cmd *exec.Cmd
	switch {
	case lines == 0:
		if !mode {
			return nil
		}
		cmd = tmuxCmd("send-keys", "-t", tty, "-X", "cancel")
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

// startHostShell starts the user's login shell on a pty; a non-empty command
// runs in it instead of a prompt (-l so the profile's PATH applies — the
// daemon's own is often slim), and the session ends when it exits.
func startHostShell(command string, cols, rows int) (hostShell, error) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		if runtime.GOOS == "darwin" {
			shell = "/bin/zsh"
		} else {
			shell = "/bin/sh"
		}
	}
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
