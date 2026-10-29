package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The agents at work, for a window that wants to say so beside what the
// work is about. Projects is the first: an issue shows the Claude Code
// and Codex sessions that are at work on it, and for how long.
//
//	GET /v1/agents/work   {"now": …, "work": [{agent, name, session, number, title, since, refs}]}
//
// A session is listed while a turn of its runs — what the session column
// marks working (markAgentStates), and a Claude Code turn that went on
// after it stopped for an answer (agentResumed). since is when that turn
// began, unix seconds beside the daemon's own now, so a browser whose
// clock is off still counts the minutes right; 0 when the daemon cannot
// tell. refs are the issue addresses the session was told: every word of
// the shape EXE-12 in what the person typed or pasted, with how many
// times that instruction said it and when it was given, the latest
// instruction first. The daemon does not know which of them are issues —
// UTF-8 has the shape too — so the reader, who does, picks: Projects
// takes the latest instruction that names an issue of its own, and of
// that the address said most.
//
// Both come from the conversation's own file, the CLI's transcript
// (agentTrail): nothing is asked of the session, and nothing is kept
// that the file does not say.

// agentRef is an address an instruction named.
type agentRef struct {
	Ref string `json:"ref"`
	N   int    `json:"n"`  // how many times that instruction said it
	At  int64  `json:"at"` // when the instruction was given
}

// agentWork is one session at work.
type agentWork struct {
	Agent   string     `json:"agent"` // "claude", "codex"
	Name    string     `json:"name"`  // "Claude Code", "Codex"
	Session string     `json:"session"`
	Number  int        `json:"number"`
	Title   string     `json:"title"` // "" while the CLI has not set one
	Since   int64      `json:"since"`
	Refs    []agentRef `json:"refs"`
}

// agentRefPattern is an issue's address as Projects writes it: a slug of
// capitals and digits, a hyphen, the issue's number.
var agentRefPattern = regexp.MustCompile(`\b[A-Z][A-Z0-9]{1,9}-[1-9][0-9]{0,8}\b`)

// agentRefsKept caps the addresses a trail remembers: the latest
// instruction's first, and of one instruction's the most said.
const agentRefsKept = 32

// agentWorkFresh is how long an answer serves every asker: a window
// asks every few seconds, and a desk and a phone may both be open.
const agentWorkFresh = 2 * time.Second

// agentTurn is what a conversation's file says of its turns and of the
// addresses its instructions named.
type agentTurn struct {
	open        bool  // a turn is in flight
	since       int64 // when it began
	ended       int64 // when the last one to end did, 0 while none has
	refs        []agentRef
	interaction int64 // Codex's latest user message, Unix milliseconds
}

// agentTrail is a conversation's file as read so far. The file only
// grows, so each read parses what was appended since the last one
// (agentTrailOf); a file shorter than what was read is another under the
// same name, and is read from its start.
type agentTrail struct {
	mu     sync.Mutex
	offset int64
	agentTurn
}

var agentTrails sync.Map // path → *agentTrail

// agentTrailOf reads a conversation's file up to its end and answers
// with what stands. line is the format's reader, one whole line at a
// time; a line still being written waits for the next read.
func agentTrailOf(path string, line func(*agentTrail, []byte)) agentTurn {
	v, _ := agentTrails.LoadOrStore(path, &agentTrail{})
	t := v.(*agentTrail)
	t.mu.Lock()
	defer t.mu.Unlock()
	if f, err := os.Open(path); err == nil {
		defer f.Close()
		if st, err := f.Stat(); err == nil && st.Size() < t.offset {
			t.offset, t.agentTurn = 0, agentTurn{}
		}
		if _, err := f.Seek(t.offset, io.SeekStart); err == nil {
			r := bufio.NewReaderSize(f, 256<<10)
			for {
				b, err := r.ReadBytes('\n')
				if err != nil {
					break
				}
				t.offset += int64(len(b))
				line(t, b)
			}
		}
	}
	turn := t.agentTurn
	turn.refs = append([]agentRef{}, t.refs...)
	return turn
}

// name notes the addresses an instruction given at ts names. An address
// belongs to the latest instruction that said it.
func (t *agentTrail) name(text string, ts int64) {
	found := agentRefPattern.FindAllString(text, -1)
	if len(found) == 0 {
		return
	}
	count := map[string]int{}
	var refs []agentRef
	for _, r := range found {
		if count[r] == 0 {
			refs = append(refs, agentRef{Ref: r, At: ts})
		}
		count[r]++
	}
	for i := range refs {
		refs[i].N = count[refs[i].Ref]
	}
	sort.SliceStable(refs, func(i, j int) bool { return refs[i].N > refs[j].N })
	for _, r := range t.refs {
		if count[r.Ref] == 0 {
			refs = append(refs, r)
		}
	}
	if len(refs) > agentRefsKept {
		refs = refs[:agentRefsKept]
	}
	t.refs = refs
}

// begin opens a turn at ts unless one is already in flight: a message
// typed while the CLI works joins the turn it lands in.
func (t *agentTrail) begin(ts int64) {
	if !t.open {
		t.open, t.since = true, ts
	}
}

// end closes the turn in flight at ts.
func (t *agentTrail) end(ts int64) {
	t.open = false
	if ts > t.ended {
		t.ended = ts
	}
}

func trailTime(s string) int64 {
	ts, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return 0
	}
	return ts.Unix()
}

// claudeTrailLine reads one line of a Claude Code transcript. A turn
// begins with a user entry that is not a tool's result — what the person
// typed or pasted, or what the CLI was woken with (a background task
// that finished) — and ends with the reply that ends it, with the
// turn's own closing line (a CLI of October 2026 writes one, earlier
// ones did not), or where the person interrupted it. The entries the
// CLI writes for itself around a slash command (flagged meta, or opening
// with a tag and without an origin) begin nothing, a sub-agent's
// entries are not the session's, and a compaction's summary is not an
// instruction. Only the person's words name addresses.
func claudeTrailLine(t *agentTrail, line []byte) {
	switch {
	case bytes.Contains(line, []byte(`"subtype":"turn_duration"`)), bytes.Contains(line, []byte(`"stop_reason":"end_turn"`)):
		var e struct {
			Type        string `json:"type"`
			Subtype     string `json:"subtype"`
			IsSidechain bool   `json:"isSidechain"`
			Timestamp   string `json:"timestamp"`
			Message     struct {
				StopReason string `json:"stop_reason"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &e) != nil || e.IsSidechain {
			return
		}
		if e.Type == "system" && e.Subtype == "turn_duration" || e.Type == "assistant" && e.Message.StopReason == "end_turn" {
			t.end(trailTime(e.Timestamp))
		}
	case bytes.Contains(line, []byte(`"type":"user"`)) && !bytes.Contains(line, []byte(`"tool_use_id":`)):
		var u struct {
			Type        string `json:"type"`
			IsMeta      bool   `json:"isMeta"`
			IsSidechain bool   `json:"isSidechain"`
			IsSummary   bool   `json:"isCompactSummary"`
			Timestamp   string `json:"timestamp"`
			Origin      *struct {
				Kind string `json:"kind"`
			} `json:"origin"`
			Message struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &u) != nil || u.Type != "user" || u.Message.Role != "user" || u.IsMeta || u.IsSidechain || u.IsSummary {
			return
		}
		text := claudeUserText(u.Message.Content)
		if text == "" {
			return
		}
		ts := trailTime(u.Timestamp)
		if strings.HasPrefix(text, "[Request interrupted") {
			t.end(ts)
			return
		}
		if u.Origin == nil && strings.HasPrefix(text, "<") {
			return // the CLI's own: a slash command, its output
		}
		t.begin(ts)
		if u.Origin == nil || u.Origin.Kind == "human" {
			t.name(text, ts)
		}
	}
}

// claudeUserText is the text of a user entry's content: the string, or
// its text blocks one under another, without the reminders the CLI puts
// in front of a prompt.
func claudeUserText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if t := strings.TrimSpace(b.Text); b.Type == "text" && t != "" && !strings.HasPrefix(t, "<system-reminder>") {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, "\n")
}

// codexTrailLine reads one line of a Codex rollout: the events that
// begin and end a turn, and the person's messages — the user items that
// are not Codex's preamble (AGENTS.md, the environment), each of which
// opens with a heading or a tag.
func codexTrailLine(t *agentTrail, line []byte) {
	event := bytes.Contains(line, []byte(`"task_started"`)) || bytes.Contains(line, []byte(`"task_complete"`)) ||
		bytes.Contains(line, []byte(`"turn_aborted"`))
	if !event && !bytes.Contains(line, []byte(`"role":"user"`)) {
		return
	}
	var e struct {
		Timestamp string `json:"timestamp"`
		Type      string `json:"type"`
		Payload   struct {
			Type    string `json:"type"`
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &e) != nil {
		return
	}
	switch {
	case e.Type == "event_msg" && e.Payload.Type == "task_started":
		t.begin(trailTime(e.Timestamp))
	case e.Type == "event_msg" && (e.Payload.Type == "task_complete" || e.Payload.Type == "turn_aborted"):
		t.end(trailTime(e.Timestamp))
	case e.Type == "response_item" && e.Payload.Type == "message" && e.Payload.Role == "user":
		var parts []string
		interaction := false
		for _, c := range e.Payload.Content {
			// The CLI writes context as user items too. Match its known
			// wrappers, not every heading or tag: a person's message can
			// be Markdown, XML, or an image without accompanying text.
			s := strings.TrimSpace(c.Text)
			if c.Type == "input_image" || s != "" && !codexContextText(s) {
				interaction = true
			}
			if s := strings.TrimSpace(c.Text); s != "" && !strings.HasPrefix(s, "<") && !strings.HasPrefix(s, "#") {
				parts = append(parts, s)
			}
		}
		if interaction {
			if ts, err := time.Parse(time.RFC3339Nano, e.Timestamp); err == nil && ts.UnixMilli() > t.interaction {
				t.interaction = ts.UnixMilli()
			}
		}
		if len(parts) > 0 {
			t.name(strings.Join(parts, "\n"), trailTime(e.Timestamp))
		}
	}
}

func codexContextText(text string) bool {
	for _, prefix := range []string{"# AGENTS.md instructions", "<environment_context>", "<permissions instructions>",
		"<collaboration_mode>", "<turn_aborted>", "<subagent_notification>", "<agent_notification>"} {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}

// claudeTrailPath is the transcript of the conversation a Claude Code
// session carries, from its status file: the hook's JSON names it, a
// resumed session's stub too (noteClaudeSession). "" before the hook has
// run, a couple of seconds into a new session.
func claudeTrailPath(statusFile string) string {
	b, err := os.ReadFile(statusFile)
	if err != nil {
		return ""
	}
	var h struct {
		SessionID      string `json:"session_id"`
		TranscriptPath string `json:"transcript_path"`
	}
	if json.Unmarshal(b, &h) != nil {
		return ""
	}
	if h.TranscriptPath != "" {
		return h.TranscriptPath
	}
	if t := claudeSessionByID(claudeHome(), h.SessionID); t != nil {
		return t.path
	}
	return ""
}

var codexRolloutPaths sync.Map // thread id → its rollout's path

// codexRolloutPath finds a thread's rollout under home: the file's name
// ends with the thread's id.
func codexRolloutPath(home, id string) string {
	if home == "" || !uuidTitle.MatchString(id) {
		return ""
	}
	if v, ok := codexRolloutPaths.Load(id); ok {
		return v.(string)
	}
	matches, _ := filepath.Glob(filepath.Join(home, "sessions", "*", "*", "*", "rollout-*-"+id+".jsonl"))
	if len(matches) == 0 {
		return ""
	}
	codexRolloutPaths.Store(id, matches[0])
	return matches[0]
}

// codexHeldRollout finds the rollout a Codex session is writing while
// its status file cannot say: Codex names its thread to the daemon at
// the end of a turn (codexArgs), so through a new conversation's first
// turn — the one that matters here, the instruction itself — only the
// process knows. It keeps the rollout open: the file is among the open
// files of the pane's process or of one it started (the npm shim runs
// the binary). A sub-agent's rollout is open there too and is passed
// over. Where /proc does not say (macOS), "": the session is known from
// its second turn on.
func codexHeldRollout(session, home string) string {
	cmd := tmuxCmd("display-message", "-p", "-t", "="+session+":", "#{pane_pid}")
	if cmd == nil || home == "" {
		return ""
	}
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || pid <= 0 {
		return ""
	}
	return heldRollout(pid, filepath.Join(home, "sessions"))
}

// heldRollout looks through a process and its descendants, a few
// generations deep, for an open rollout under dir that is a thread of
// the user's own.
func heldRollout(pid int, dir string) string {
	pids, seen := []int{pid}, map[int]bool{pid: true}
	for depth := 0; depth < 4 && len(pids) > 0; depth++ {
		var next []int
		for _, p := range pids {
			proc := filepath.Join("/proc", strconv.Itoa(p))
			fds, _ := os.ReadDir(filepath.Join(proc, "fd"))
			for _, fd := range fds {
				path, err := os.Readlink(filepath.Join(proc, "fd", fd.Name()))
				if err != nil || !strings.HasPrefix(path, dir+string(filepath.Separator)) {
					continue
				}
				if name := filepath.Base(path); !strings.HasPrefix(name, "rollout-") || !strings.HasSuffix(name, ".jsonl") {
					continue
				}
				if r := readCodexRollout(path); r != nil && r.user {
					return path
				}
			}
			// a child is listed under the thread that started it
			tasks, _ := os.ReadDir(filepath.Join(proc, "task"))
			for _, task := range tasks {
				b, _ := os.ReadFile(filepath.Join(proc, "task", task.Name(), "children"))
				for _, f := range strings.Fields(string(b)) {
					if c, err := strconv.Atoi(f); err == nil && !seen[c] {
						seen[c] = true
						next = append(next, c)
					}
				}
			}
		}
		pids = next
	}
	return ""
}

var codexHeld sync.Map // session and when it was created → the rollout found for it

// agentTrailFor is what the conversation of one of an agent's sessions
// says, nothing when its file is not known.
func (s *Server) agentTrailFor(a hostAgent, ss agentSession) agentTurn {
	status := s.agentStatusFile(a, ss.Name)
	if !a.notify {
		if path := claudeTrailPath(status); path != "" {
			return agentTrailOf(path, claudeTrailLine)
		}
		return agentTurn{}
	}
	home := codexHome()
	path := codexRolloutPath(home, readAgentThreadID(status))
	if path == "" {
		key := ss.Name + ":" + strconv.FormatInt(ss.Created, 10)
		if v, ok := codexHeld.Load(key); ok {
			path = v.(string)
		} else if path = codexHeldRollout(ss.Name, home); path != "" {
			codexHeld.Store(key, path)
		}
	}
	if path == "" {
		return agentTurn{}
	}
	return agentTrailOf(path, codexTrailLine)
}

// agentResumeMargin is how long after a state word the pane may still be
// printing what came before it: tmux stamps whole seconds, and the hook
// that writes the word runs beside the redraw it announces.
// agentResumeWindow is how lately the pane must have printed: a turn in
// flight keeps its spinner turning.
const (
	agentResumeMargin = 2
	agentResumeWindow = 10 * 60
)

// agentResumed says whether a Claude Code session whose hooks' last word
// is not "working" has a turn in flight all the same. The hooks say when
// a turn stops for the person — a permission, a question — and when it
// ends, but have no word for the answer, nor for a turn the CLI began by
// itself (a background task that finished): the word stays as it was
// while the work goes on. Two things together say it does: the pane has
// printed since the word was written, and lately (the dialog went away,
// the spinner is back), and the transcript has a turn open. The
// transcript is read only for a session that passes the first, which
// leaves the hundreds that sit at their prompts unread.
func (s *Server) agentResumed(a hostAgent, ss agentSession, now int64) bool {
	if a.notify || ss.State == "" || ss.State == agentStateWorking {
		return false
	}
	st, err := os.Stat(s.agentStateFile(a, ss.Name))
	if err != nil || ss.Activity <= st.ModTime().Unix()+agentResumeMargin || now-ss.Activity > agentResumeWindow {
		return false
	}
	return s.agentTrailFor(a, ss).open
}

// agentTurnOver says whether the turn a session's hooks called working
// is over all the same. An interrupted turn — Escape on a running one —
// runs no Stop hook, so the word stands until the next prompt, and the
// column's dot with it (EXE-2: one session had said so for three
// weeks). The transcript says what the hook could not: a turn that
// ended after the word was written, and none open since. A session
// whose transcript is not known keeps the word.
func (s *Server) agentTurnOver(a hostAgent, ss agentSession) bool {
	st, err := os.Stat(s.agentStateFile(a, ss.Name))
	if err != nil {
		return false
	}
	turn := s.agentTrailFor(a, ss)
	return !turn.open && turn.ended >= st.ModTime().Unix()
}

// agentsAtWork lists the sessions at work across the agents, the one
// longest at it first.
func (s *Server) agentsAtWork() []agentWork {
	work := []agentWork{}
	for _, app := range []string{"claude", "codex"} {
		a := hostAgents[app]
		list := s.agentSessions(a)
		s.markAgentStates(a, list)
		work = append(work, s.agentWorkOf(a, list, time.Now().Unix())...)
	}
	sort.SliceStable(work, func(i, j int) bool {
		if (work[i].Since == 0) != (work[j].Since == 0) {
			return work[j].Since == 0
		}
		return work[i].Since < work[j].Since
	})
	return work
}

// agentWorkOf picks the sessions at work out of an agent's list, its
// states marked (markAgentStates), with what each conversation's file
// says of the turn. The title's spinner is a turn in flight whatever the
// file shows yet. The hooks' "working" is too: markAgentStates has let
// go of one whose file shows the turn ending after the word was written
// (agentTurnOver), and the file, read again here, may show that only
// now. A session that only printed something lately — a repaint as a
// window attached, a key typed at the prompt — needs the file to show a
// turn in flight.
func (s *Server) agentWorkOf(a hostAgent, list []agentSession, now int64) []agentWork {
	var work []agentWork
	for _, ss := range list {
		if !ss.Working && !s.agentResumed(a, ss, now) {
			continue
		}
		turn := s.agentTrailFor(a, ss)
		since := turn.since
		if !turn.open {
			// a turn the file does not show yet, or shows in a way this
			// reader does not know: it began when the hook wrote its word
			since = 0
			if ss.State == agentStateWorking {
				st, err := os.Stat(s.agentStateFile(a, ss.Name))
				if err != nil || turn.ended >= st.ModTime().Unix() {
					continue
				}
				since = st.ModTime().Unix()
			} else if !ss.spinner {
				continue
			}
		}
		refs := turn.refs
		if refs == nil {
			refs = []agentRef{}
		}
		work = append(work, agentWork{Agent: a.app, Name: a.title, Session: ss.Name, Number: ss.Number,
			Title: strings.TrimSpace(strings.TrimPrefix(ss.Title, "✳")), Since: since, Refs: refs})
	}
	return work
}

func (s *Server) handleAgentWork(w http.ResponseWriter, r *http.Request) {
	s.agentWorkMu.Lock()
	if time.Since(s.agentWorkAt) > agentWorkFresh {
		s.agentWorkList, s.agentWorkAt = s.agentsAtWork(), time.Now()
	}
	work := s.agentWorkList
	s.agentWorkMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"now": time.Now().Unix(), "work": work})
}
