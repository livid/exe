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
// when installed, Windows PowerShell otherwise. A non-empty command runs in
// it instead of a prompt and the session ends when it exits.
func startHostShell(command string, cols, rows int) (hostShell, error) {
	shell := "powershell.exe"
	if p, err := exec.LookPath("pwsh.exe"); err == nil {
		shell = p
	}
	line := fmt.Sprintf(`"%s"`, shell)
	if command != "" {
		line = fmt.Sprintf(`"%s" -NoLogo -Command %s`, shell, command)
	}
	opts := []conpty.ConPtyOption{
		conpty.ConPtyDimensions(cols, rows),
		conpty.ConPtyEnv(append(os.Environ(), "TERM=xterm-256color")),
	}
	if home, err := os.UserHomeDir(); err == nil {
		opts = append(opts, conpty.ConPtyWorkDir(home))
	}
	pty, err := conpty.Start(line, opts...)
	if err != nil {
		return nil, err
	}
	return &windowsShell{pty: pty}, nil
}

// startAgent runs an agent's CLI (Claude Code, Codex) on a ConPTY in the
// project dir. No tmux on Windows — each window is a fresh CLI run — and
// no status-line hook: its bridge is a sh one-liner (agentStatusArgs).
func (s *Server) startAgent(a hostAgent, cols, rows int) (hostShell, error) {
	bin := agentPath(a)
	if bin == "" {
		return nil, fmt.Errorf("%s is not installed on this host", a.title)
	}
	opts := []conpty.ConPtyOption{
		conpty.ConPtyDimensions(cols, rows),
		conpty.ConPtyEnv(cliEnv(bin)),
		conpty.ConPtyWorkDir(s.agentProjectDir()),
	}
	pty, err := conpty.Start(fmt.Sprintf(`"%s"`, bin), opts...)
	if err != nil {
		return nil, err
	}
	return &windowsShell{pty: pty}, nil
}
