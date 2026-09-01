package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/coder/websocket"
)

// hostShell is an interactive shell on a local pseudo-terminal. Close ends
// the session and reaps the shell process.
type hostShell interface {
	io.ReadWriteCloser
	Resize(cols, rows int)
}

// claudePath finds the Claude Code CLI. The daemon often runs with a slim
// PATH (Supervisor, launchd), so the usual per-user and system install
// locations are checked directly when LookPath misses.
func claudePath() string {
	if p, err := exec.LookPath("claude"); err == nil {
		return p
	}
	candidates := []string{"/usr/local/bin/claude", "/opt/homebrew/bin/claude"}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append([]string{
			filepath.Join(home, ".local", "bin", "claude"),
			filepath.Join(home, ".claude", "local", "claude"),
		}, candidates...)
	}
	for _, p := range candidates {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

// claudeProjectDir picks Claude Code's working directory: the exe checkout
// when this host carries one, the shared Workspace folder otherwise.
func (s *Server) claudeProjectDir() string {
	if st, err := os.Stat("/www/exe"); err == nil && st.IsDir() {
		return "/www/exe"
	}
	return s.workspaceDir()
}

// claudeEnv is the daemon's environment with TERM set and the CLI's own
// directory on PATH — claude's subshells inherit this, and the daemon's
// PATH may not carry the user-level install location.
func claudeEnv(claude string) []string {
	env := append(os.Environ(), "TERM=xterm-256color")
	dir := filepath.Dir(claude)
	for i, kv := range env {
		if !strings.HasPrefix(kv, "PATH=") {
			continue
		}
		for _, p := range filepath.SplitList(kv[len("PATH="):]) {
			if p == dir {
				return env
			}
		}
		env[i] = "PATH=" + dir + string(os.PathListSeparator) + kv[len("PATH="):]
		return env
	}
	return append(env, "PATH="+dir)
}

// handleHostTerminal bridges a browser WebSocket to a login shell on the
// host itself — the desktop's Terminal window. Same wire protocol as the
// VM terminal: binary frames carry terminal bytes both ways, text frames
// carry control messages ({"resize":[cols,rows]}). ?app=claude runs the
// Claude Code CLI instead of a shell — the desktop's Claude Code icon.
// ?cmd=<command line> runs that one command in a login shell — the desktop
// menu's "terminal <command>" shortcut to a CLI tool; the session ends
// with the command.
func (s *Server) handleHostTerminal(w http.ResponseWriter, r *http.Request) {
	var sh hostShell
	var err error
	if r.URL.Query().Get("app") == "claude" {
		sh, err = s.startClaudeCode(80, 24)
	} else {
		sh, err = startHostShell(r.URL.Query().Get("cmd"), 80, 24)
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"*"}})
	if err != nil {
		sh.Close()
		return
	}
	c.SetReadLimit(1 << 20)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer sh.Close()
	defer c.CloseNow()

	out := &wsWriter{ctx: ctx, c: c}
	go func() {
		io.Copy(out, sh) // pty output → browser; EOF when the shell exits
		c.Close(websocket.StatusNormalClosure, "session ended")
		cancel()
	}()

	for {
		typ, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		switch typ {
		case websocket.MessageBinary:
			if _, err := sh.Write(data); err != nil {
				return
			}
		case websocket.MessageText:
			var msg struct {
				Resize []int `json:"resize"`
			}
			if json.Unmarshal(data, &msg) == nil && len(msg.Resize) == 2 {
				sh.Resize(msg.Resize[0], msg.Resize[1])
			}
		}
	}
}
