//go:build windows

package server

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/UserExistsError/conpty"
)

type windowsShell struct {
	pty *conpty.ConPty
}

func (s *windowsShell) Read(p []byte) (int, error)  { return s.pty.Read(p) }
func (s *windowsShell) Write(p []byte) (int, error) { return s.pty.Write(p) }
func (s *windowsShell) Resize(cols, rows int)       { s.pty.Resize(cols, rows) }
func (s *windowsShell) Close() error                { return s.pty.Close() }

// startHostShell starts an interactive PowerShell on a ConPTY — PowerShell 7
// when installed, Windows PowerShell otherwise.
func startHostShell(cols, rows int) (hostShell, error) {
	shell := "powershell.exe"
	if p, err := exec.LookPath("pwsh.exe"); err == nil {
		shell = p
	}
	opts := []conpty.ConPtyOption{
		conpty.ConPtyDimensions(cols, rows),
		conpty.ConPtyEnv(append(os.Environ(), "TERM=xterm-256color")),
	}
	if home, err := os.UserHomeDir(); err == nil {
		opts = append(opts, conpty.ConPtyWorkDir(home))
	}
	pty, err := conpty.Start(fmt.Sprintf(`"%s"`, shell), opts...)
	if err != nil {
		return nil, err
	}
	return &windowsShell{pty: pty}, nil
}

// startClaudeCode runs the Claude Code CLI on a ConPTY in the project dir.
// No tmux on Windows — each window is a fresh CLI run.
func (s *Server) startClaudeCode(cols, rows int) (hostShell, error) {
	claude := claudePath()
	if claude == "" {
		return nil, fmt.Errorf("Claude Code is not installed on this host")
	}
	opts := []conpty.ConPtyOption{
		conpty.ConPtyDimensions(cols, rows),
		conpty.ConPtyEnv(claudeEnv(claude)),
		conpty.ConPtyWorkDir(s.claudeProjectDir()),
	}
	pty, err := conpty.Start(fmt.Sprintf(`"%s"`, claude), opts...)
	if err != nil {
		return nil, err
	}
	return &windowsShell{pty: pty}, nil
}
