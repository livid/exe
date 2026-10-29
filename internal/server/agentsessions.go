package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
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
// titles are the panes' titles: Claude Code keeps its on the current
// task, and Codex, launched with its title on the thread (codexArgs),
// on the thread's name — with a spinner in front while a turn runs,
// which the list strips and reads as working. A row is marked when
// tmux's bell flag is up for its session: the CLI rang the bell — a
// reply finished, or a permission is waited on — while no window was
// looking. A session whose pane printed output in the last few seconds
// is working too — the CLI streams and repaints its spinner for the
// whole of a turn, and falls silent at a prompt — so the column can mark
// rows still going as the bell marks rows that want someone.

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
	// when a client last landed on the session — tmux stamps it on every
	// attach and switch-client, whole seconds, 0 for a session no window
	// has shown yet: how lastAgentSession finds where the window was
	lastAttached int64
	// Codex's last user message, in Unix milliseconds; terminal output
	// and switching to a session never advance its place in the column.
	interaction int64
	// from the session's hooks (agentStateHooks), Claude Code only: State
	// is the word they wrote, Wants that the session waits for the person
	// — its turn finished, or a permission or question is pending
	State string `json:"state,omitempty"`
	Wants bool   `json:"wants"`
	// the conversation exists for the CLI's own resume picker
	// (readAgentResumable): the column offers Archive only then
	Resumable bool `json:"resumable"`
	// a restart of the host ended the session: no tmux session holds it,
	// and a switch to it continues its conversation (agentrestart.go)
	Stopped bool `json:"stopped,omitempty"`
	// the pane title carried a spinner: a turn is in flight, whatever an
	// older word in the state file says (markAgentStates)
	spinner bool
}

// agentWorkingSeconds: pane output this fresh means the session's CLI is
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
// survive the trip. The activity stamp is the window's, which pane
// output moves; the session's moves on keys from a client alone, so it
// would never see a CLI at work in a session no window shows.
const tmuxSessionFormat = "#{session_name}:#{session_created}:#{window_activity}:#{session_attached}:#{window_bell_flag}:#{session_last_attached}:#{pane_title}"

// uuidTitle matches the title Codex puts on the terminal before the
// thread has a name: the thread's id.
var uuidTitle = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// parseAgentSessions picks the agent's sessions out of list-panes output
// in tmuxSessionFormat, the latest first: the one started most
// recently at the top of the column, where a new conversation opens,
// and the icon's own — the oldest, unless it was started over — at the
// bottom; two started within a second go by number. untitled lists what a pane is
// titled when its program has set no title of its own — the hostname,
// tmux's default, and the project folder's name, Codex's default before
// the daemon put the thread's name there — which counts as none, as
// does a thread id; now is the clock Working weighs each session's
// activity against. A spinner in front of a title (Codex, while a turn
// runs) is stripped and read as working.
func parseAgentSessions(a hostAgent, out string, untitled []string, now int64) []agentSession {
	list := []agentSession{}
	seen := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.SplitN(line, ":", 7)
		if len(f) < 7 || seen[f[0]] {
			continue
		}
		n := agentSessionNumber(a, f[0])
		if n == 0 {
			continue
		}
		seen[f[0]] = true
		title, spinner := strings.TrimSpace(f[6]), false
		if r, size := utf8.DecodeRuneInString(title); r >= 0x2800 && r <= 0x28ff { // braille: a spinner frame
			title, spinner = strings.TrimSpace(title[size:]), true
		}
		if slices.Contains(untitled, title) || uuidTitle.MatchString(title) {
			title = ""
		}
		created, _ := strconv.ParseInt(f[1], 10, 64)
		activity, _ := strconv.ParseInt(f[2], 10, 64)
		lastAttached, _ := strconv.ParseInt(f[5], 10, 64)
		list = append(list, agentSession{Name: f[0], Number: n, Title: title, Created: created,
			Activity: activity, Attached: f[3] != "0" && f[3] != "", Bell: f[4] == "1",
			Working: spinner || activity > 0 && now-activity <= agentWorkingSeconds, spinner: spinner,
			lastAttached: lastAttached})
	}
	sortAgentSessions(list)
	return list
}

// sortAgentSessions puts the latest interaction first when known, then
// the latest start, with the session number breaking ties within a second.
// parseAgentSessions has no interaction times yet: it returns start order.
func sortAgentSessions(list []agentSession) {
	sort.Slice(list, func(i, j int) bool {
		if list[i].interaction != list[j].interaction {
			return list[i].interaction > list[j].interaction
		}
		if list[i].Created != list[j].Created {
			return list[i].Created > list[j].Created
		}
		return list[i].Number > list[j].Number
	})
}

// orderSessionColumn applies Codex's message order after the stopped
// sessions have joined the live ones. Claude Code keeps its start order
// and day groups. Missing conversations have no interaction timestamp.
func (s *Server) orderSessionColumn(a hostAgent, list []agentSession) {
	if a.app != "codex" {
		return
	}
	for i := range list {
		list[i].interaction = s.agentTrailFor(a, list[i]).interaction
	}
	sortAgentSessions(list)
}

// agentPanes is every pane on the agents' tmux server in
// tmuxSessionFormat, and what counts as no title there
// (parseAgentSessions); an error without tmux, or without a server
// running.
func (s *Server) agentPanes() (out string, untitled []string, err error) {
	cmd := tmuxCmd("list-panes", "-a", "-F", tmuxSessionFormat)
	if cmd == nil {
		return "", nil, errors.New("no tmux on this host")
	}
	b, err := cmd.Output()
	if err != nil {
		return "", nil, err
	}
	host, _ := os.Hostname()
	return string(b), []string{host, filepath.Base(s.agentProjectDir())}, nil
}

// agentSessions lists an agent's sessions on this host's tmux server —
// none without tmux, or without a server running.
func (s *Server) agentSessions(a hostAgent) []agentSession {
	out, untitled, err := s.agentPanes()
	if err != nil {
		return []agentSession{}
	}
	return parseAgentSessions(a, out, untitled, time.Now().Unix())
}

// lastAgentSession is the session an agent's window showed last, where
// a reopened window lands (startAgent) — from any browser, and after
// the daemon restarts, so it is tmux's own record that decides: the
// session a client landed on most recently. That stamp is whole
// seconds, so two rows clicked within one tie, and the daemon's memory
// of the last window's session (agentLast) settles that while it has
// one; "" when no session of the agent's has been shown yet.
func (s *Server) lastAgentSession(a hostAgent) string {
	s.agentLastMu.Lock()
	remembered := s.agentLast[a.app]
	s.agentLastMu.Unlock()
	return pickLastSession(s.agentSessions(a), remembered)
}

// pickLastSession is lastAgentSession's choice over a list in number
// order: the latest stamp, ties to remembered, else the first.
func pickLastSession(list []agentSession, remembered string) string {
	var best agentSession
	for _, l := range list {
		if l.lastAttached > best.lastAttached || l.lastAttached == best.lastAttached && l.lastAttached > 0 && l.Name == remembered {
			best = l
		}
	}
	return best.Name
}

// rememberAgentSession notes the session an agent's window is on, for
// lastAgentSession's tiebreak.
func (s *Server) rememberAgentSession(a hostAgent, name string) {
	s.agentLastMu.Lock()
	if s.agentLast == nil {
		s.agentLast = map[string]string{}
	}
	s.agentLast[a.app] = name
	s.agentLastMu.Unlock()
}

// markAgentStates reads each session's state file into the list. A
// word from the hooks outranks the activity guess: working means
// working whatever the pane's silence (a long tool run), and a finished
// or waiting session is not working however much it repaints. One word
// the hooks cannot take back: a turn the person interrupts runs no Stop
// hook, so "working" stands until the next prompt — for that word alone
// the conversation's transcript is asked whether the turn is over
// (agentTurnOver), and the row shows no mark then, as the person who
// pressed Escape saw it stop.
func (s *Server) markAgentStates(a hostAgent, list []agentSession) {
	for i := range list {
		list[i].Resumable = readAgentResumable(s.agentStatusFile(a, list[i].Name))
		if list[i].spinner {
			continue // the title's spinner: a turn is in flight, whatever the last word was
		}
		switch st := readAgentState(s.agentStateFile(a, list[i].Name)); st {
		case agentStateWorking:
			list[i].State, list[i].Working = st, !s.agentTurnOver(a, list[i])
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
	s        *Server
	a        hostAgent
	sh       agentShell
	out      textSender // the window's socket; nil in tests that only drive the sessions
	mu       sync.Mutex
	cur      string
	kick     chan struct{}
	settling sync.WaitGroup // archives whose figures are still to be sent
}

// waitArchived returns once every archive in flight has sent what the
// session gave back, or after the settling time has passed: the
// socket's close waits on it (handleHostTerminal), so Archive of the
// last session still gets its figures through.
func (c *agentColumn) waitArchived() {
	done := make(chan struct{})
	go func() { c.settling.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(archiveSettle + time.Second):
	}
}

// newAgentColumn starts a window's column on the session its client
// was attached to (startAgent) — the client may not have registered
// with tmux yet, so that is what the first frame says until follow
// re-reads it.
// textSender is the window's side of the link, as the column needs it:
// a text frame, in order with the terminal bytes (wsWriter).
type textSender interface {
	WriteText([]byte) error
}

func newAgentColumn(s *Server, a hostAgent, sh agentShell, session string, out textSender) *agentColumn {
	c := &agentColumn{s: s, a: a, sh: sh, out: out, kick: make(chan struct{}, 1)}
	c.cur = session
	if cur := sh.Current(); cur != "" {
		c.cur = cur
	}
	s.rememberAgentSession(a, c.cur)
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
	c.s.rememberAgentSession(c.a, name)
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
		list := c.s.agentSessions(c.a)
		c.s.markAgentStates(c.a, list)
		list = c.s.withStopped(c.a, list) // and those a host restart ended (agentrestart.go)
		c.s.orderSessionColumn(c.a, list)
		frame := map[string]any{"sessions": list, "current": c.current()}
		if !c.a.notify {
			frame["prompt"] = c.s.agentLastPrompt(c.a, c.current()) // the line over the terminal (claudeprompts.go)
		}
		if threads := c.s.agentThreads(c.a, list); threads != nil {
			frame["threads"] = threads // conversations started elsewhere (codexthreads.go, claudesessions.go)
		}
		msg, _ := json.Marshal(frame)
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

// switchTo moves the window to one of the agent's sessions, continuing
// it first when a host restart had stopped it (wakeAgentSession).
func (c *agentColumn) switchTo(name string) error {
	if agentSessionNumber(c.a, name) == 0 {
		return fmt.Errorf("not a %s session: %q", c.a.title, name)
	}
	if _, err := c.s.wakeAgentSession(c.a, name); err != nil {
		c.refresh() // a conversation gone from the machine took its row
		return err
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
// exits. What the session gave back — the memory its processes held,
// read before they are ended, and what the machine has available once
// they are gone (agentmemory.go) — follows as {"archived":name,…}, from
// a goroutine of its own: the processes take about a second to go, and
// the window's keys must not wait on them.
func (c *agentColumn) archive(name string) error {
	n := agentSessionNumber(c.a, name)
	if n == 0 {
		return fmt.Errorf("not a %s session: %q", c.a.title, name)
	}
	if c.s.forgetStopped(c.a, name) {
		// a session a host restart stopped: nothing runs, nothing is freed
		c.refresh()
		if c.out != nil {
			msg, _ := json.Marshal(map[string]string{"archived": name})
			c.out.WriteText(msg)
		}
		return nil
	}
	if name == c.current() {
		// the neighbours by number, whatever order the list is in
		var prev, next string
		var pn, nn int
		for _, s := range c.s.agentSessions(c.a) {
			if s.Number < n && s.Number > pn {
				prev, pn = s.Name, s.Number
			} else if s.Number > n && (nn == 0 || s.Number < nn) {
				next, nn = s.Name, s.Number
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
	m := measureSession(name)
	// held before the kill: the last session's end closes the socket as
	// soon as its client is gone, and that close waits on it (waitArchived)
	c.settling.Add(1)
	if out, err := cmd.CombinedOutput(); err != nil {
		c.settling.Done()
		return fmt.Errorf("tmux kill-session: %s", strings.TrimSpace(string(out)))
	}
	os.Remove(c.s.agentStatusFile(c.a, name)) // the next session under this number starts clean
	os.Remove(c.s.agentStateFile(c.a, name))
	c.s.unnoteAgentSession(c.a, name)
	c.refresh()
	go func() {
		defer c.settling.Done()
		f := m.settle()
		msg, _ := json.Marshal(struct {
			Archived string `json:"archived"`
			archiveFigures
		}{name, f})
		if c.out != nil {
			c.out.WriteText(msg)
		}
	}()
	return nil
}

// open starts a new session of the agent's, numbered after the highest
// live one, and moves the window to it.
func (c *agentColumn) open() error {
	return c.openIn("")
}

// openIn is open in a folder of the caller's — the one an app named over
// the desk's bridge (Projects' Open in Claude Code, with the project's
// working directory) — and the project folder when dir is "". A folder
// that is not on this machine is refused rather than quietly swapped for
// the project folder: the caller asked for that one.
func (c *agentColumn) openIn(dir string) error {
	return c.openTask(dir, "")
}

// maxAgentTask bounds a session's first message. It reaches the CLI as
// one argument (newAgentTaskSession), and Linux takes none over 128 KiB;
// Projects says the same number when an issue is longer.
const maxAgentTask = 64 << 10

// agentTaskOK checks a first message before anything is started for it.
func agentTaskOK(task string) error {
	if len(task) > maxAgentTask {
		return fmt.Errorf("a first message is at most 64 KiB; this one is %d KiB", (len(task)+1023)>>10)
	}
	if strings.ContainsRune(task, 0) {
		return fmt.Errorf("a first message cannot hold a NUL character")
	}
	return nil
}

// openTask is openIn with a first message for the new session (an issue
// Projects sends to the agent), "" for none.
func (c *agentColumn) openTask(dir, task string) error {
	if err := agentTaskOK(task); err != nil {
		return err
	}
	dir, err := agentSessionDir(dir)
	if err != nil {
		return err
	}
	name := c.s.nextAgentSession(c.a)
	if err := c.s.newAgentTaskSession(c.a, name, dir, task); err != nil {
		return err
	}
	return c.switchTo(name)
}

// agentSessionDir checks a folder a session was asked to open in: "" is
// no wish (the project folder), anything else is a whole path to a folder
// that exists. The message names the path the way a person would read it.
func agentSessionDir(dir string) (string, error) {
	if dir == "" {
		return "", nil
	}
	if !filepath.IsAbs(dir) {
		return "", fmt.Errorf("dir: %q is not a whole path", dir)
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return "", fmt.Errorf("%s is not a folder on this machine", dir)
	}
	return filepath.Clean(dir), nil
}

// nextAgentSession names the agent's next numbered session: one past
// the highest in use, 2 at the least (the icon's own is 1). A stopped
// session's number is in use: its name and status file wait for it.
func (s *Server) nextAgentSession(a hostAgent) string {
	n := 2
	for _, ss := range s.agentSessions(a) {
		if ss.Number >= n {
			n = ss.Number + 1
		}
	}
	for _, st := range s.stoppedSessions(a) {
		if st.Number >= n {
			n = st.Number + 1
		}
	}
	return agentSessionName(a, n)
}

// wakeAll continues every session a host restart stopped, the window
// staying where it is: the rows turn live as they start, and a failure
// comes back as {"error":…}.
func (c *agentColumn) wakeAll() {
	go func() {
		if err := c.s.wakeAllStopped(c.a, c.refresh); err != nil && c.out != nil {
			b, _ := json.Marshal(map[string]string{"error": err.Error()})
			c.out.WriteText(b)
		}
	}()
}

// resume continues a conversation started elsewhere — a Codex thread
// (codexthreads.go), a Claude Code session (claudesessions.go) — in a
// session of its own, in the folder it was started in when that still
// exists, and moves the window there. The session is marked with the
// conversation at once (noteCodexThread, noteClaudeSession), so its row
// leaves the list as the session's arrives.
func (c *agentColumn) resume(id string) error {
	if !agentIDPattern.MatchString(id) {
		return errors.New("resume: not a thread id")
	}
	name := c.s.nextAgentSession(c.a)
	if c.a.notify {
		if err := c.s.newAgentSessionIn(c.a, name, codexThreadDir(id), "resume", id); err != nil {
			return err
		}
		c.s.noteCodexThread(c.a, name, id)
		return c.switchTo(name)
	}
	t := claudeSessionByID(claudeHome(), id)
	if t == nil {
		return fmt.Errorf("no %s session %s on this machine", c.a.title, id)
	}
	if err := c.s.newAgentSessionIn(c.a, name, existingDir(t.cwd), "--resume", id); err != nil {
		return err
	}
	c.s.noteClaudeSession(c.a, name, id, t.path)
	return c.switchTo(name)
}

// existingDir is dir when it is a folder that still exists, "" otherwise
// (a session then opens in the project folder, as any other).
func existingDir(dir string) string {
	if st, err := os.Stat(dir); dir != "" && err == nil && st.IsDir() {
		return dir
	}
	return ""
}

// codexThreadDir is the folder a thread was started in, "" when it is
// gone (the session then opens in the project folder, as any other).
func codexThreadDir(id string) string {
	for _, t := range codexAppThreads(codexHome(), nil, time.Now(), 0) {
		if t.ID == id {
			return existingDir(t.Cwd)
		}
	}
	return ""
}
