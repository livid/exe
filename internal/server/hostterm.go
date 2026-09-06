package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/coder/websocket"
)

// hostShell is an interactive shell on a local pseudo-terminal. Close ends
// the session and reaps the shell process.
type hostShell interface {
	io.ReadWriteCloser
	Resize(cols, rows int)
}

// agentShell is an agent window's pty: a tmux client the window's
// session column moves between the agent's sessions (agentsessions.go).
// Current names the session the client shows, "" when it is no tmux
// client; Switch moves it to another session of the agent's.
type agentShell interface {
	hostShell
	Current() string
	Switch(session string) error
}

// tmuxSocket names the tmux server the agent sessions live on: "" for
// the user's default server, a -L socket name in tests so they never
// touch it.
var tmuxSocket string

// tmuxCmd is a tmux command on that server, nil without tmux on PATH.
func tmuxCmd(args ...string) *exec.Cmd {
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		return nil
	}
	if tmuxSocket != "" {
		args = append([]string{"-L", tmuxSocket}, args...)
	}
	return exec.Command(tmux, args...)
}

// hostAgent is an agent CLI the desktop opens in a window of its own: app
// is the ?app= value the browser sends, bin the command name, title the
// name shown to people, and session the tmux session that keeps the
// conversation alive between windows. homeDirs are extra per-user install
// locations (relative to $HOME) beyond the usual ones cliPath checks.
// statusLine marks a CLI with a status-line hook the window's status line
// can draw the session's figures from; openaiUsage one that runs on the
// ChatGPT sign-in, whose window's status line shows the subscription's
// usage windows instead (both agentstatus.go). notify marks Codex, which
// has no such hook but runs a notify command at the end of each turn and
// takes the terminal title and bell settings the session column reads
// (codexArgs, agentstatus.go).
type hostAgent struct {
	app, bin, title, session        string
	homeDirs                        []string
	statusLine, openaiUsage, notify bool
}

var hostAgents = map[string]hostAgent{
	"claude": {app: "claude", bin: "claude", title: "Claude Code", session: "exe-claude",
		homeDirs: []string{filepath.Join(".claude", "local")}, statusLine: true},
	"codex": {app: "codex", bin: "codex", title: "Codex", session: "exe-codex", openaiUsage: true, notify: true},
}

// agentPath finds an agent's CLI on this host, "" when it is not installed.
func agentPath(a hostAgent) string { return cliPath(a.bin, a.homeDirs...) }

// claudePath finds the Claude Code CLI.
func claudePath() string { return agentPath(hostAgents["claude"]) }

// cliPath finds a CLI by name. The daemon often runs with a slim PATH
// (Supervisor, launchd), so when LookPath misses the usual install
// locations are checked directly: the per-user ones first — ~/.local/bin,
// any homeDirs, bun and npm's own global prefix, then every nvm node
// version's bin, newest first (npm -g installs land there; the shim needs
// the node beside it, which cliEnv puts on PATH) — then /usr/local/bin and
// Homebrew.
func cliPath(name string, homeDirs ...string) string {
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	var dirs []string
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".local", "bin"))
		for _, d := range homeDirs {
			dirs = append(dirs, filepath.Join(home, d))
		}
		dirs = append(dirs, filepath.Join(home, ".bun", "bin"), filepath.Join(home, ".npm-global", "bin"))
		if nvm, _ := filepath.Glob(filepath.Join(home, ".nvm", "versions", "node", "*", "bin")); len(nvm) > 0 {
			sort.Sort(sort.Reverse(sort.StringSlice(nvm))) // v24 before v22 (string order: fine for two-digit majors)
			dirs = append(dirs, nvm...)
		}
	}
	dirs = append(dirs, "/usr/local/bin", "/opt/homebrew/bin")
	for _, d := range dirs {
		p := filepath.Join(d, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

// agentProjectDir picks an agent's working directory: the exe checkout
// when this host carries one, the shared Workspace folder otherwise.
func (s *Server) agentProjectDir() string {
	if st, err := os.Stat("/www/exe"); err == nil && st.IsDir() {
		return "/www/exe"
	}
	return s.workspaceDir()
}

// cliPATH is the daemon's PATH with the CLI's own directory in front —
// the CLI's subshells inherit it, the daemon's PATH may not carry the
// user-level install location, and an npm shim (codex under nvm) needs
// the node beside it.
func cliPATH(bin string) string {
	path := os.Getenv("PATH")
	dir := filepath.Dir(bin)
	for _, p := range filepath.SplitList(path) {
		if p == dir {
			return path
		}
	}
	if path == "" {
		return dir
	}
	return dir + string(os.PathListSeparator) + path
}

// cliEnv is the daemon's environment with TERM set and PATH as cliPATH.
func cliEnv(bin string) []string {
	env := []string{"TERM=xterm-256color", "PATH=" + cliPATH(bin)}
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "PATH=") && !strings.HasPrefix(kv, "TERM=") {
			env = append(env, kv)
		}
	}
	return env
}

// shQuote single-quotes s for a shell command line — sh, bash, zsh and
// fish all read '…' literally and all accept '\” for a quote inside.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// handleHostTerminal bridges a browser WebSocket to a login shell on the
// host itself — the desktop's Terminal window. Same wire protocol as the
// VM terminal: binary frames carry terminal bytes both ways, text frames
// carry control messages ({"resize":[cols,rows]}). ?app=claude or
// ?app=codex runs that agent's CLI instead of a shell — the desktop's
// Claude Code and Codex icons (hostAgents); an agent window also gets
// text frames the other way for its status line (agentstatus.go):
// {"status":…} with the session's figures from Claude Code's status-line
// hook, {"usage":…} with the ChatGPT subscription's usage windows for
// Codex; and for its session column (agentsessions.go)
// {"sessions":[…],"current":…}, the agent's tmux sessions and the one the
// window shows. The window sends {"switch":"exe-claude-2"} to move to
// another, {"new":true} to start one and {"archive":"exe-claude-2"} to
// end one; what goes wrong comes back as {"error":…}.
// ?cmd=<command line> runs that one command in a login shell — the desktop
// menu's "terminal <command>" shortcut to a CLI tool; the session ends
// with the command.
func (s *Server) handleHostTerminal(w http.ResponseWriter, r *http.Request) {
	var sh hostShell
	var err error
	var agent *hostAgent
	var ash agentShell
	if app := r.URL.Query().Get("app"); app != "" {
		a, ok := hostAgents[app]
		if !ok {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("unknown app %q", app))
			return
		}
		if ash, err = s.startAgent(a, 80, 24); err == nil {
			sh = ash
		}
		agent = &a
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
	var col *agentColumn
	if agent != nil {
		col = newAgentColumn(s, *agent, ash, out)
		go col.follow(ctx)
		if agent.statusLine {
			go pushAgentStatus(ctx, out, col.statusFile)
		}
		if agent.openaiUsage {
			go pushOpenAIUsage(ctx, out, s.codexUsage, openaiUsageEvery)
		}
	}

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
				Resize  []int  `json:"resize"`
				Switch  string `json:"switch"`
				New     bool   `json:"new"`
				Archive string `json:"archive"`
			}
			if json.Unmarshal(data, &msg) != nil {
				continue
			}
			if len(msg.Resize) == 2 {
				sh.Resize(msg.Resize[0], msg.Resize[1])
			}
			if col == nil || (msg.Switch == "" && !msg.New && msg.Archive == "") {
				continue
			}
			var err error
			switch {
			case msg.Switch != "":
				err = col.switchTo(msg.Switch)
			case msg.Archive != "":
				err = col.archive(msg.Archive)
			default:
				err = col.open()
			}
			if err != nil {
				b, _ := json.Marshal(map[string]string{"error": err.Error()})
				out.WriteText(b)
			}
		}
	}
}
