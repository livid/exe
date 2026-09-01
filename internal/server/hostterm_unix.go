//go:build !windows

package server

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"time"

	"github.com/creack/pty"
)

type unixShell struct {
	f   *os.File
	cmd *exec.Cmd
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
// project dir. With tmux on the host the CLI lives inside a persistent
// session of its own ("exe-claude", "exe-codex"): closing the window only
// detaches, and the desktop icon returns to the running conversation. -A
// attaches when the session already exists, -D kicks any stale client so
// the pty size follows the newest window. The command line goes through
// env so the CLI's own directory is on PATH inside the session too: a
// tmux server's environment is fixed when it starts (by the first agent
// opened), and an npm shim like codex under nvm needs the node beside it.
// Without tmux each window is a fresh CLI run.
func (s *Server) startAgent(a hostAgent, cols, rows int) (hostShell, error) {
	bin := agentPath(a)
	if bin == "" {
		return nil, fmt.Errorf("%s is not installed on this host", a.title)
	}
	dir := s.agentProjectDir()
	cmd := exec.Command(bin)
	if tmux, err := exec.LookPath("tmux"); err == nil {
		line := "env " + shQuote("PATH="+cliPATH(bin)) + " " + shQuote(bin)
		cmd = exec.Command(tmux, "new-session", "-A", "-D", "-s", a.session, "-c", dir, line)
	}
	cmd.Dir = dir
	cmd.Env = cliEnv(bin)
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
