package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// An agent window has a session column: the agent's tmux sessions on this
// host listed down the left, the terminal on the right, so a second
// Claude Code conversation opens beside the first instead of over it.
// The icon's own session ("exe-claude") is number 1; the column adds
// numbered ones after it ("exe-claude-2", …), each a detached tmux
// session with the CLI launched as the first was (newAgentSession,
// hostterm_unix.go) and a status file of its own. The window's tmux
// client is what moves: switch-client on the pty that is already
// attached, so one window and one WebSocket serve every session. Row
// titles are the panes' titles, which Claude Code keeps on its current
// task, and a row is marked when tmux's bell flag is up for its session:
// the CLI rang the bell — a reply finished, or a permission is waited
// on — while no window was looking. A session whose pane printed output
// in the last few seconds is working — the CLI streams and repaints its
// spinner for the whole of a turn, and falls silent at a prompt — so the
// column can mark rows still going as the bell marks rows that want
// someone.

// agentSession is one row of the column.
type agentSession struct {
	Name     string `json:"name"`
	Number   int    `json:"number"`
	Title    string `json:"title"` // "" while the CLI has not set one
	Created  int64  `json:"created"`
	Activity int64  `json:"activity"`
	Attached bool   `json:"attached"`
	Bell     bool   `json:"bell"`
	Working  bool   `json:"working"`
	// from the session's hooks (agentStateHooks), Claude Code only: State
	// is the word they wrote, Wants that the session waits for the person
	// — its turn finished, or a permission or question is pending
	State string `json:"state,omitempty"`
	Wants bool   `json:"wants"`
	// the conversation exists for the CLI's own resume picker
	// (readAgentResumable): the column offers Archive only then
	Resumable bool `json:"resumable"`
}

// agentWorkingSeconds: activity this fresh means the session's CLI is
// mid-turn. The list is polled every two seconds and tmux stamps whole
// seconds, so the window has to be a few of them wide.
const agentWorkingSeconds = 5

// agentSessionName is the tmux session for an agent's nth session: the
// icon's own for 1, numbered after it from 2.
func agentSessionName(a hostAgent, n int) string {
	if n <= 1 {
		return a.session
	}
	return fmt.Sprintf("%s-%d", a.session, n)
}

// agentSessionNumber is the number behind a session name of the agent's,
// 0 for any other session on the tmux server.
func agentSessionNumber(a hostAgent, name string) int {
	if name == a.session {
		return 1
	}
	rest, ok := strings.CutPrefix(name, a.session+"-")
	if !ok {
		return 0
	}
	n, err := strconv.Atoi(rest)
	if err != nil || n < 2 || strconv.Itoa(n) != rest {
		return 0
	}
	return n
}

// tmuxSessionFormat is the list-panes format agentSessions reads. Colons
// separate the fields and the pane title, free text, comes last: a
// session name cannot hold a colon, and tmux prints control characters
// in its output as octal escapes, so no unprintable separator would
// survive the trip.
const tmuxSessionFormat = "#{session_name}:#{session_created}:#{session_activity}:#{session_attached}:#{window_bell_flag}:#{pane_title}"

// parseAgentSessions picks the agent's sessions out of list-panes output
// in tmuxSessionFormat, in number order. hostname is what tmux titles a
// pane whose program has set no title, which counts as none; now is the
// clock Working weighs each session's activity against.
func parseAgentSessions(a hostAgent, out, hostname string, now int64) []agentSession {
	list := []agentSession{}
	seen := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.SplitN(line, ":", 6)
		if len(f) < 6 || seen[f[0]] {
			continue
		}
		n := agentSessionNumber(a, f[0])
		if n == 0 {
			continue
		}
		seen[f[0]] = true
		title := strings.TrimSpace(f[5])
		if title == hostname {
			title = ""
		}
		created, _ := strconv.ParseInt(f[1], 10, 64)
		activity, _ := strconv.ParseInt(f[2], 10, 64)
		list = append(list, agentSession{Name: f[0], Number: n, Title: title, Created: created,
			Activity: activity, Attached: f[3] != "0" && f[3] != "", Bell: f[4] == "1",
			Working: activity > 0 && now-activity <= agentWorkingSeconds})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Number < list[j].Number })
	return list
}

// agentSessions lists an agent's sessions on this host's tmux server —
// none without tmux, or without a server running.
func agentSessions(a hostAgent) []agentSession {
	cmd := tmuxCmd("list-panes", "-a", "-F", tmuxSessionFormat)
	if cmd == nil {
		return []agentSession{}
	}
	out, err := cmd.Output()
	if err != nil {
		return []agentSession{}
	}
	host, _ := os.Hostname()
	return parseAgentSessions(a, string(out), host, time.Now().Unix())
}

// markAgentStates reads each session's state file into the list. A
// word from the hooks outranks the activity guess: working means
// working whatever the pane's silence (a long tool run), and a finished
// or waiting session is not working however much it repaints.
func (s *Server) markAgentStates(a hostAgent, list []agentSession) {
	for i := range list {
		list[i].Resumable = readAgentResumable(s.agentStatusFile(a, list[i].Name))
		switch st := readAgentState(s.agentStateFile(a, list[i].Name)); st {
		case agentStateWorking:
			list[i].State, list[i].Working = st, true
		case agentStateDone, agentStateWaiting:
			list[i].State, list[i].Working, list[i].Wants = st, false, true
		}
	}
}

// agentColumn is the daemon's side of one window's column. It keeps the
// window's list current — {"sessions":[…],"current":"…"} text frames on
// connect, after a switch, and whenever tmux reports a change, polled
// every two seconds as the titles follow the CLI's task — moves the
// window's tmux client between sessions, and starts new ones.
type agentColumn struct {
	s    *Server
	a    hostAgent
	sh   agentShell
	out  *wsWriter
	mu   sync.Mutex
	cur  string
	kick chan struct{}
}

func newAgentColumn(s *Server, a hostAgent, sh agentShell, out *wsWriter) *agentColumn {
	c := &agentColumn{s: s, a: a, sh: sh, out: out, kick: make(chan struct{}, 1)}
	c.cur = a.session
	if cur := sh.Current(); cur != "" {
		c.cur = cur
	}
	return c
}

func (c *agentColumn) current() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cur
}

func (c *agentColumn) setCurrent(name string) {
	c.mu.Lock()
	c.cur = name
	c.mu.Unlock()
}

// statusFile is the current session's status file: the window's status
// line follows it across switches (pushAgentStatus).
func (c *agentColumn) statusFile() string {
	return c.s.agentStatusFile(c.a, c.current())
}

// refresh asks follow for a list right now rather than at the next tick.
func (c *agentColumn) refresh() {
	select {
	case c.kick <- struct{}{}:
	default:
	}
}

// follow sends the list for the life of the window, each time it or the
// current session changes. The current session is re-read from tmux on
// every pass: the user may have moved the client from inside tmux.
func (c *agentColumn) follow(ctx context.Context) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	var last string
	for {
		if cur := c.sh.Current(); cur != "" {
			c.setCurrent(cur)
		}
		list := agentSessions(c.a)
		c.s.markAgentStates(c.a, list)
		msg, _ := json.Marshal(map[string]any{"sessions": list, "current": c.current()})
		if string(msg) != last {
			last = string(msg)
			if c.out.WriteText(msg) != nil {
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-c.kick:
		case <-t.C:
		}
	}
}

// switchTo moves the window to one of the agent's sessions.
func (c *agentColumn) switchTo(name string) error {
	if agentSessionNumber(c.a, name) == 0 {
		return fmt.Errorf("not a %s session: %q", c.a.title, name)
	}
	if err := c.sh.Switch(name); err != nil {
		return err
	}
	c.setCurrent(name)
	c.refresh()
	return nil
}

// archive ends one of the agent's sessions: the tmux session and the
// CLI in it. The conversation stays on disk, where the CLI's own resume
// picker finds it — the desktop keeps no list of its own. When it is the
// session the window shows, the window moves to a neighbour first — the
// session before it, else the one after — so it stays open; the last
// session goes without one, and the window ends as it does when the CLI
// exits.
func (c *agentColumn) archive(name string) error {
	n := agentSessionNumber(c.a, name)
	if n == 0 {
		return fmt.Errorf("not a %s session: %q", c.a.title, name)
	}
	if name == c.current() {
		var prev, next string
		for _, s := range agentSessions(c.a) {
			if s.Number < n {
				prev = s.Name
			} else if s.Number > n && next == "" {
				next = s.Name
			}
		}
		if other := prev; other != "" || next != "" {
			if other == "" {
				other = next
			}
			if err := c.switchTo(other); err != nil {
				return err
			}
		}
	}
	cmd := tmuxCmd("kill-session", "-t", "="+name)
	if cmd == nil {
		return fmt.Errorf("sessions need tmux on this host")
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux kill-session: %s", strings.TrimSpace(string(out)))
	}
	os.Remove(c.s.agentStatusFile(c.a, name)) // the next session under this number starts clean
	os.Remove(c.s.agentStateFile(c.a, name))
	c.refresh()
	return nil
}

// open starts a new session of the agent's, numbered after the highest
// live one, and moves the window to it.
func (c *agentColumn) open() error {
	n := 2
	for _, s := range agentSessions(c.a) {
		if s.Number >= n {
			n = s.Number + 1
		}
	}
	name := agentSessionName(c.a, n)
	if err := c.s.newAgentSession(c.a, name); err != nil {
		return err
	}
	return c.switchTo(name)
}
