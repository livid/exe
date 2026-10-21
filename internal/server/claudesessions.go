package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Claude Code sessions started outside the desktop — claude run in a
// Terminal window, over SSH, in an IDE — are conversations on this
// machine all the same: transcripts under ~/.claude/projects, one
// <session id>.jsonl per session in a folder named after its working
// directory. The Claude Code window's column lists the latest of them
// after its tmux sessions, as the Codex window lists the threads from
// the ChatGPT app (codexthreads.go), so a click can continue one here
// (agentColumn.resume: `claude --resume <id>` in a session of its own,
// in the session's own folder). Headless runs (`claude -p`: the hub
// watcher's turns, the daemon's own) are not listed — their entries
// carry the SDK entrypoint — nor are sessions the column's own tmux
// sessions carry (their status files name them: noteClaudeSession,
// readAgentThreadID), nor sessions without a first prompt yet. A session
// whose CLI is still running somewhere — Claude Code keeps a record per
// process under ~/.claude/sessions — is marked open there.

// claudeSessionsShown caps the rows: the latest sessions by activity.
const claudeSessionsShown = 10

// claudeHome is Claude Code's config folder: $CLAUDE_CONFIG_DIR, ~/.claude
// by default.
func claudeHome() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude")
}

// claudeTranscript is what a session's transcript says about it. The id,
// folder, start, entrypoint and first prompt are fixed once written; the
// title can arrive later (the CLI names a session after its first turns,
// /rename puts the person's own name on it), so a transcript that grew is
// read again (claudeTranscriptOf keys the cache on the size read).
type claudeTranscript struct {
	path, id, cwd, entrypoint string
	created                   int64
	prompt                    string // the first prompt: the title when the CLI has named it nothing
	title                     string // the person's own name for it, else the CLI's
	size                      int64
}

var claudeMetaCache sync.Map // path → *claudeTranscript

// claudeHeadless says whether a transcript's entrypoint is a headless
// run's — `claude -p`, or the SDK — which the column does not list.
func claudeHeadless(entrypoint string) bool {
	return strings.HasPrefix(entrypoint, "sdk")
}

// claudeOrigin names where a session was started, from its entrypoint:
// the CLI's own is a terminal.
func claudeOrigin(e string) string {
	if e == "" || e == "cli" {
		return "a terminal"
	}
	return e
}

// claudeTranscriptOf is a transcript's reading, from the cache when the
// file is the size it was read at; a headless run's stays read once, as
// nothing else about it matters.
func claudeTranscriptOf(path string, size int64) *claudeTranscript {
	if v, ok := claudeMetaCache.Load(path); ok {
		if t := v.(*claudeTranscript); t.size == size || claudeHeadless(t.entrypoint) {
			return t
		}
	}
	t := readClaudeTranscript(path, size)
	if t != nil {
		claudeMetaCache.Store(path, t)
	}
	return t
}

// readClaudeTranscript reads a transcript's user entries for the
// session's folder, start, entrypoint and first prompt — the entries
// before the prompt are the CLI's own: a caveat, a slash command and its
// output, each flagged meta or opening with a tag — and its title lines,
// the CLI's ai-title and the person's custom-title, the last of each.
// nil for a file with no user entry yet. Lines are read up to 16 MB, a
// paste's worth; a longer one ends the read with what was found.
func readClaudeTranscript(path string, size int64) *claudeTranscript {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16<<20)
	t := &claudeTranscript{path: path, id: strings.TrimSuffix(filepath.Base(path), ".jsonl"), size: size}
	var ai, custom string
	seen := false
	for sc.Scan() {
		line := sc.Bytes()
		switch {
		case bytes.Contains(line, []byte(`"type":"user"`)):
			if seen && t.prompt != "" {
				continue // the fixed facts are in; only titles can still come
			}
			var u struct {
				Type       string `json:"type"`
				IsMeta     bool   `json:"isMeta"`
				Entrypoint string `json:"entrypoint"`
				Cwd        string `json:"cwd"`
				Timestamp  string `json:"timestamp"`
				Message    struct {
					Role    string          `json:"role"`
					Content json.RawMessage `json:"content"`
				} `json:"message"`
			}
			if json.Unmarshal(line, &u) != nil || u.Type != "user" || u.Message.Role != "user" {
				continue
			}
			seen = true
			if t.cwd == "" {
				t.cwd = u.Cwd
			}
			if t.entrypoint == "" {
				t.entrypoint = u.Entrypoint
			}
			if t.created == 0 {
				if ts, err := time.Parse(time.RFC3339Nano, u.Timestamp); err == nil {
					t.created = ts.Unix()
				}
			}
			if t.prompt == "" && !u.IsMeta {
				t.prompt = claudePrompt(u.Message.Content)
			}
		case bytes.Contains(line, []byte(`"type":"ai-title"`)):
			var l struct {
				Type  string `json:"type"`
				Title string `json:"aiTitle"`
			}
			if json.Unmarshal(line, &l) == nil && l.Type == "ai-title" && l.Title != "" {
				ai = l.Title
			}
		case bytes.Contains(line, []byte(`"type":"custom-title"`)):
			var l struct {
				Type  string `json:"type"`
				Title string `json:"customTitle"`
			}
			if json.Unmarshal(line, &l) == nil && l.Type == "custom-title" && l.Title != "" {
				custom = l.Title
			}
		}
	}
	if !seen {
		return nil
	}
	if t.title = custom; t.title == "" {
		t.title = ai
	}
	t.title = firstLine(t.title, 60)
	return t
}

// claudePrompt is the person's text in a user entry's content — a
// string, or the first text block of a list — "" for the CLI's own
// entries, which open with a tag (<command-name>, <system-reminder>).
func claudePrompt(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return claudePromptText(s)
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	for _, b := range blocks {
		if b.Type == "text" {
			if t := claudePromptText(b.Text); t != "" {
				return t
			}
		}
	}
	return ""
}

func claudePromptText(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || strings.HasPrefix(s, "<") {
		return ""
	}
	if said, ok := watcherPromptInput(s); ok { // the posts in a hub watcher's build prompt (claudeprompts.go)
		s = said
	}
	return firstLine(s, 60)
}

// claudeTranscriptFiles is every session transcript under home, latest
// write first: <home>/projects/<folder>/<session id>.jsonl, and nothing
// deeper (a session's sub-agents keep transcripts in a folder of its own).
type claudeTranscriptFile struct {
	path  string
	mtime time.Time
	size  int64
}

func claudeTranscriptFiles(home string) []claudeTranscriptFile {
	var files []claudeTranscriptFile
	root := filepath.Join(home, "projects")
	projects, _ := os.ReadDir(root)
	for _, p := range projects {
		if !p.IsDir() {
			continue
		}
		entries, _ := os.ReadDir(filepath.Join(root, p.Name()))
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".jsonl") || !uuidTitle.MatchString(strings.TrimSuffix(name, ".jsonl")) {
				continue
			}
			if info, err := e.Info(); err == nil {
				files = append(files, claudeTranscriptFile{filepath.Join(root, p.Name(), name), info.ModTime(), info.Size()})
			}
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mtime.After(files[j].mtime) })
	return files
}

// claudeSessions lists the sessions started elsewhere, latest activity
// first, at most limit of them (0: all); live are session ids the
// column's sessions already carry, left out, open the ids a CLI process
// still holds (claudeOpenSessions). A transcript is read only when its
// id passes: the column's own sessions cost nothing.
func claudeSessions(home string, live, open map[string]bool, now time.Time, limit int) []agentThread {
	out := []agentThread{}
	if home == "" {
		return out
	}
	for _, f := range claudeTranscriptFiles(home) {
		if id := strings.TrimSuffix(filepath.Base(f.path), ".jsonl"); live[id] {
			continue
		}
		t := claudeTranscriptOf(f.path, f.size)
		if t == nil || claudeHeadless(t.entrypoint) || t.prompt == "" {
			continue
		}
		title, prompt := t.title, t.prompt
		if title == "" {
			title, prompt = t.prompt, ""
		}
		out = append(out, agentThread{ID: t.id, Title: title, Prompt: prompt, Origin: claudeOrigin(t.entrypoint), Cwd: t.cwd,
			Created: t.created, Activity: f.mtime.Unix(), Working: now.Sub(f.mtime) <= agentWorkingSeconds*time.Second,
			Open: open[t.id]})
		if len(out) == limit {
			break
		}
	}
	return out
}

// claudeSessionByID finds one session's transcript by its id, nil when
// there is none — what a resume needs: the file, and the folder it was
// started in.
func claudeSessionByID(home, id string) *claudeTranscript {
	if home == "" || !uuidTitle.MatchString(id) {
		return nil
	}
	matches, _ := filepath.Glob(filepath.Join(home, "projects", "*", id+".jsonl"))
	for _, path := range matches {
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		if t := claudeTranscriptOf(path, info.Size()); t != nil {
			return t
		}
	}
	return nil
}

// claudeOpenSessions is the ids of the sessions a Claude Code process
// still holds: the CLI keeps a record per process under sessions/, with
// the process's start time, so a pid reused since it exited is not
// taken for it.
func claudeOpenSessions(home string) map[string]bool {
	open := map[string]bool{}
	if home == "" {
		return open
	}
	dir := filepath.Join(home, "sessions")
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var r struct {
			Pid       int    `json:"pid"`
			SessionID string `json:"sessionId"`
			ProcStart string `json:"procStart"`
		}
		if json.Unmarshal(b, &r) != nil || r.Pid <= 0 || r.SessionID == "" {
			continue
		}
		if processAlive(r.Pid, r.ProcStart) {
			open[r.SessionID] = true
		}
	}
	return open
}

// processAlive says whether pid is running and, where /proc tells (Linux),
// was started at procStart — the 22nd field of /proc/<pid>/stat, which is
// what the record keeps; a pid the kernel has handed out again since is
// another process. Elsewhere a signal of zero asks the kernel.
func processAlive(pid int, procStart string) bool {
	if b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat"); err == nil {
		// the command name sits in parentheses and may hold spaces: the
		// fields count from the last ')' — state is the 3rd, starttime the 22nd
		s := string(b)
		i := strings.LastIndexByte(s, ')')
		if i < 0 {
			return false
		}
		f := strings.Fields(s[i+1:])
		if len(f) < 20 {
			return false
		}
		return procStart == "" || f[19] == procStart
	} else if !os.IsNotExist(err) {
		return false
	}
	if _, err := os.Stat("/proc/self"); err == nil {
		return false // /proc is there and the pid is not
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

// noteClaudeSession marks a session as carrying a conversation from the
// start — one resumed from the column — so its row leaves the list as
// the session arrives and Archive is offered at once: a stub of the
// status-line JSON the hook overwrites within seconds of launch
// (readAgentThreadID, readAgentResumable).
func (s *Server) noteClaudeSession(a hostAgent, session, id, transcript string) {
	file := s.agentStatusFile(a, session)
	os.MkdirAll(filepath.Dir(file), 0o755)
	b, _ := json.Marshal(map[string]string{"session_id": id, "transcript_path": transcript})
	os.WriteFile(file, b, 0o644)
}
