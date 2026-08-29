//go:build !windows

package server

import (
	"errors"
	"os"
	"os/exec"
	"runtime"

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

func (s *unixShell) Close() error {
	err := s.f.Close()
	s.cmd.Process.Kill()
	s.cmd.Wait()
	return err
}

// startClaudeCode runs the Claude Code CLI on a pty in the project dir.
// With tmux on the host the CLI lives inside a persistent "exe-claude"
// session: closing the window only detaches, and the desktop icon returns
// to the running conversation. -A attaches when the session already
// exists, -D kicks any stale client so the pty size follows the newest
// window. Without tmux each window is a fresh CLI run.
func (s *Server) startClaudeCode(cols, rows int) (hostShell, error) {
	claude := claudePath()
	if claude == "" {
		return nil, errors.New("Claude Code is not installed on this host")
	}
	dir := s.claudeProjectDir()
	cmd := exec.Command(claude)
	if tmux, err := exec.LookPath("tmux"); err == nil {
		cmd = exec.Command(tmux, "new-session", "-A", "-D", "-s", "exe-claude", "-c", dir, claude)
	}
	cmd.Dir = dir
	cmd.Env = claudeEnv(claude)
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		return nil, err
	}
	return &unixShell{f: f, cmd: cmd}, nil
}

// startHostShell starts the user's login shell on a pty.
func startHostShell(cols, rows int) (hostShell, error) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		if runtime.GOOS == "darwin" {
			shell = "/bin/zsh"
		} else {
			shell = "/bin/sh"
		}
	}
	cmd := exec.Command(shell, "-l")
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
