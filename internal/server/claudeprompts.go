package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"
)

// Every prompt of a Claude Code conversation, for the line over the
// window's terminal and the list it opens (index.html, the Claude Code
// window's .sess-last): the line shows the latest prompt of the session
// on screen, and a click lists them all. A prompt is what the person
// typed or pasted, whole, its text blocks one under another — the first
// line cut to claudeInfoLine is Get Info's reading and the line's — so
// the list reads as the person's side of the conversation. The transcript
// is read as it grows (claudePromptsOf keeps where each read stopped),
// as the trails of the agents at work are (agentTrailOf): the column's
// frames ask for the session on screen every two seconds, and the first
// reading of a long transcript is the only one that costs.
//
//	GET /v1/agents/{app}/sessions/{name}/prompts   {"prompts":[{text, at, cut}]}

// claudePromptEntry is one prompt of a conversation.
type claudePromptEntry struct {
	Text string `json:"text"`
	At   int64  `json:"at"`            // when it was given, unix seconds; 0 when the entry carries no time
	Cut  bool   `json:"cut,omitempty"` // the text ran past claudePromptMax and is cut there
}

// claudePromptMax is how much of one prompt is kept, in runes: a paste
// of a whole file is cut, a long instruction is not.
const claudePromptMax = 20000

// claudePromptsFile is a transcript as read so far.
type claudePromptsFile struct {
	mu      sync.Mutex
	offset  int64
	prompts []claudePromptEntry
}

var claudePromptsCache sync.Map // path → *claudePromptsFile

// claudePromptsOf is every prompt of the transcript at path so far, in
// order; nil when the file cannot be read or holds none yet. Only what
// was appended since the last read is parsed; a file shorter than what
// was read is another under the same name, and is read from its start.
// The slice is the cache's own and is only ever appended to: a caller
// reads it and does not change it.
func claudePromptsOf(path string) []claudePromptEntry {
	v, _ := claudePromptsCache.LoadOrStore(path, &claudePromptsFile{})
	c := v.(*claudePromptsFile)
	c.mu.Lock()
	defer c.mu.Unlock()
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	if st, err := f.Stat(); err == nil && st.Size() < c.offset {
		c.offset, c.prompts = 0, nil
	}
	if _, err := f.Seek(c.offset, io.SeekStart); err != nil {
		return c.prompts
	}
	r := bufio.NewReaderSize(f, 256<<10)
	for {
		b, err := r.ReadBytes('\n')
		if err != nil {
			break // a line still being written waits for the next read
		}
		c.offset += int64(len(b))
		if p, ok := claudePromptLine(b); ok {
			c.prompts = append(c.prompts, p)
		}
	}
	return c.prompts
}

// claudePromptLine reads one transcript line for a prompt: a user entry
// that is the person's — not the CLI's own (flagged meta, or one of the
// wrappers it writes, claudeOwnText), not a tool's result, not a
// sub-agent's, and not what a background task woke the CLI with (an
// origin other than human) — with its text blocks one under another and
// the reminders the CLI puts in front left out (claudeUserText).
func claudePromptLine(line []byte) (claudePromptEntry, bool) {
	if !bytes.Contains(line, []byte(`"type":"user"`)) {
		return claudePromptEntry{}, false
	}
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
		return claudePromptEntry{}, false
	}
	if u.Origin != nil && u.Origin.Kind != "human" {
		return claudePromptEntry{}, false
	}
	if claudeToolTurn(u.Message.Content) {
		return claudePromptEntry{}, false
	}
	text := claudeUserText(u.Message.Content)
	if text == "" || claudeOwnText(text) {
		return claudePromptEntry{}, false
	}
	if said, ok := watcherPromptInput(text); ok {
		text = said
	}
	p := claudePromptEntry{Text: text, At: trailTime(u.Timestamp)}
	if utf8.RuneCountInString(text) > claudePromptMax {
		r := []rune(text)
		p.Text, p.Cut = strings.TrimSpace(string(r[:claudePromptMax]))+"…", true
	}
	return p, true
}

// claudeToolTurn says whether a user entry's content carries a tool's
// result: then the entry is the tool's turn, not the person's, whatever
// text rides along.
func claudeToolTurn(raw json.RawMessage) bool {
	var blocks []struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return false
	}
	for _, b := range blocks {
		if b.Type == "tool_result" {
			return true
		}
	}
	return false
}

// claudeOwnTags open the user entries the CLI writes for itself — a
// slash command and its output, a shell command typed with "!", the
// reminders it puts before a prompt, a background task's notice, what an
// IDE told it — which an older CLI left without an origin to say so.
var claudeOwnTags = []string{"<command-name>", "<command-message>", "<command-args>",
	"<local-command-stdout>", "<local-command-stderr>", "<local-command-caveat>",
	"<bash-input>", "<bash-stdout>", "<bash-stderr>",
	"<system-reminder>", "<task-notification>", "<user-memory-input>", "<ide_"}

// claudeOwnText says whether a user entry's text is the CLI's rather
// than the person's: one of its wrappers, the note it leaves where a
// turn was interrupted, or the summary a compaction puts in the person's
// place. A prompt that opens with pasted HTML or XML is the person's.
func claudeOwnText(s string) bool {
	for _, tag := range claudeOwnTags {
		if strings.HasPrefix(s, tag) {
			return true
		}
	}
	return strings.HasPrefix(s, "[Request interrupted") ||
		strings.HasPrefix(s, "This session is being continued from a previous conversation")
}

// The hub watcher (~/.claude/hub/watch, agentapi.go) opens a column
// session on a build prompt of its own: a page of standing orders —
// "You are Claude's hub watcher, in build mode: …", "You are Claude's
// watcher, in build mode. …" for a comment in Projects — around the posts
// or comments that woke it, each an item — "- Comment <id> on issue
// <address> by <name>" for a comment — with its facts two spaces in and
// what the person wrote four spaces in, under "Text:":
//
//	New post(s):
//	- Post <id> by <name>
//	  JSON: …
//	  Text:
//	    what the person wrote
//
// It pastes a later one into the same session behind a line of its
// own ("Hub watcher, …"), which the CLI keeps with the paste wrapped in
// <pasted_content …>. What the person said is the text of those items, so
// the line over the terminal, Prompts and Get Info show that, one item
// under another, and a title read from the first prompt is its first
// line. A prompt in which no item is found stays whole.
var watcherItem = regexp.MustCompile(`^- (Post \S+|Comment \S+ on issue \S+) by .`)

func watcherPromptInput(s string) (string, bool) {
	const orders = "You are Claude's "
	body := s
	if strings.HasPrefix(s, "Hub watcher") {
		_, paste, ok := strings.Cut(s, "<pasted_content")
		if !ok {
			return s, false
		}
		_, body, _ = strings.Cut(paste, ">")
		body = strings.TrimLeft(body, "\n")
	}
	if first, _, _ := strings.Cut(body, "\n"); !strings.HasPrefix(first, orders) || !strings.Contains(first, " watcher") {
		return s, false
	}
	var said []string
	lines := strings.Split(body, "\n")
	for i := 0; i < len(lines); i++ {
		if !watcherItem.MatchString(lines[i]) {
			continue
		}
		for i++; i < len(lines) && strings.HasPrefix(lines[i], "  ") && lines[i] != "  Text:"; i++ {
		}
		if i == len(lines) || lines[i] != "  Text:" {
			i--
			continue
		}
		var text []string
		for i++; i < len(lines) && (strings.HasPrefix(lines[i], "    ") || strings.TrimSpace(lines[i]) == ""); i++ {
			text = append(text, strings.TrimPrefix(lines[i], "    "))
		}
		i-- // the line that ended the text may open the next item
		if t := strings.TrimSpace(strings.Join(text, "\n")); t != "" {
			said = append(said, t)
		}
	}
	if len(said) == 0 {
		return s, false
	}
	return strings.Join(said, "\n\n"), true
}

// agentLastPrompt is what the column's frame says of the session on
// screen, for the line over the terminal: how many prompts it has, and
// the latest one's first line and time. Claude Code only; N is 0 while
// the session has no conversation, or no prompt yet.
type agentLastPrompt struct {
	Session string `json:"session"`
	N       int    `json:"n"`
	Text    string `json:"text,omitempty"`
	At      int64  `json:"at,omitempty"`
}

func (s *Server) agentLastPrompt(a hostAgent, name string) agentLastPrompt {
	lp := agentLastPrompt{Session: name}
	if a.notify || name == "" {
		return lp
	}
	path := claudeTrailPath(s.agentStatusFile(a, name))
	if path == "" {
		return lp
	}
	ps := claudePromptsOf(path)
	if len(ps) == 0 {
		return lp
	}
	last := ps[len(ps)-1]
	lp.N, lp.Text, lp.At = len(ps), firstLine(last.Text, claudeInfoLine), last.At
	return lp
}

// handleAgentSessionPrompts answers with every prompt of one of the
// column's sessions, from its transcript (claudePromptsOf). Claude Code
// only: Codex keeps no transcript the daemon reads this far.
func (s *Server) handleAgentSessionPrompts(w http.ResponseWriter, r *http.Request) {
	a, ok := s.agentOf(w, r)
	if !ok {
		return
	}
	name := r.PathValue("name")
	if agentSessionNumber(a, name) == 0 {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("not a %s session: %q", a.title, name))
		return
	}
	if a.notify {
		writeErr(w, http.StatusNotFound, fmt.Errorf("no prompts of a %s session yet", a.title))
		return
	}
	path := claudeTrailPath(s.agentStatusFile(a, name))
	if path == "" {
		writeErr(w, http.StatusNotFound, errors.New("no conversation yet"))
		return
	}
	ps := claudePromptsOf(path)
	if ps == nil {
		ps = []claudePromptEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"prompts": ps})
}
