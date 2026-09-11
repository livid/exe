package server

import (
	"bufio"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Codex threads started outside the desktop — in the ChatGPT app on a
// phone (its remote Codex runs on this host's app-server), the Codex
// app, VS Code — are conversations on this machine all the same: rollout
// files under $CODEX_HOME/sessions. The Codex window's column lists the
// latest of them after its tmux sessions, so a click can continue one
// here (agentColumn.resume: `codex resume <id>` in a session of its
// own). The CLI's own threads (originator codex-tui: the desktop's
// sessions, and codex run in a terminal) are not listed — the tmux rows
// are those, and old ones are the CLI's resume picker's business — nor
// are sub-agent threads (a guardian review, a spawned agent), which
// belong to their parent, nor threads a session of the column's already
// carries (noteCodexThread).

// codexThread is one such thread as the column shows it.
type codexThread struct {
	ID       string `json:"thread"`
	Title    string `json:"title"`
	Origin   string `json:"origin"` // where it was started, in words
	Cwd      string `json:"cwd"`
	Created  int64  `json:"created"`
	Activity int64  `json:"activity"` // the rollout's last write
	Working  bool   `json:"working"`  // written to in the last few seconds: a turn runs there
}

// codexThreadsShown caps the rows: the latest threads by activity.
const codexThreadsShown = 10

// codexHome is $CODEX_HOME, ~/.codex by default.
func codexHome() string {
	if p := codexConfigPath(); p != "" {
		return filepath.Dir(p)
	}
	return ""
}

// codexRollout is what a rollout's first line says about its thread,
// with its first prompt, read once per file (codexMetaCache): neither
// changes after the thread starts.
type codexRollout struct {
	id, originator, cwd string
	created             int64
	user                bool   // a thread of the user's own, not a sub-agent's
	prompt              string // the first prompt, the title when the index has no name
}

var codexMetaCache sync.Map // path → *codexRollout

// codexOwnOriginator says whether a thread was started by the CLI
// itself — the TUI (the desktop's sessions, codex in a terminal) or a
// non-interactive exec run — which the column does not list.
func codexOwnOriginator(o string) bool {
	return o == "codex-tui" || o == "codex_cli_rs" || strings.Contains(o, "exec")
}

// codexOrigin names where a thread was started, from its originator.
func codexOrigin(o string) string {
	switch {
	case strings.Contains(o, "chatgpt"):
		return "the ChatGPT app"
	case strings.Contains(o, "desktop"):
		return "the Codex app"
	case strings.Contains(o, "vscode"):
		return "VS Code"
	}
	return o
}

// readCodexRollout reads a rollout's session_meta line and, for a thread
// of the user's own, its first prompt: the user items before it are
// Codex's preamble — the plugin list, AGENTS.md, the environment — each
// opening with a tag or a heading. Lines are read up to a megabyte in;
// the prompt is the fourth or so.
func readCodexRollout(path string) *codexRollout {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	if !sc.Scan() {
		return nil
	}
	var meta struct {
		Type    string `json:"type"`
		Payload struct {
			ID           string          `json:"id"`
			Timestamp    string          `json:"timestamp"`
			Cwd          string          `json:"cwd"`
			Originator   string          `json:"originator"`
			Source       json.RawMessage `json:"source"`
			ThreadSource string          `json:"thread_source"`
			Parent       string          `json:"parent_thread_id"`
		} `json:"payload"`
	}
	if json.Unmarshal(sc.Bytes(), &meta) != nil || meta.Type != "session_meta" || meta.Payload.ID == "" {
		return nil
	}
	p := meta.Payload
	r := &codexRollout{id: p.ID, originator: p.Originator, cwd: p.Cwd}
	if t, err := time.Parse(time.RFC3339Nano, p.Timestamp); err == nil {
		r.created = t.Unix()
	}
	// a sub-agent's source is an object, its thread_source names the
	// review, and it points at its parent
	r.user = p.Parent == "" && (p.ThreadSource == "" || p.ThreadSource == "user") &&
		(len(p.Source) == 0 || p.Source[0] == '"')
	if !r.user || codexOwnOriginator(r.originator) {
		return r
	}
	var read int
	for sc.Scan() && read < 1<<20 {
		line := sc.Bytes()
		read += len(line)
		if !strings.Contains(string(line), `"role":"user"`) {
			continue
		}
		var item struct {
			Payload struct {
				Role    string `json:"role"`
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"payload"`
		}
		if json.Unmarshal(line, &item) != nil || item.Payload.Role != "user" {
			continue
		}
		for _, c := range item.Payload.Content {
			t := strings.TrimSpace(c.Text)
			if t == "" || strings.HasPrefix(t, "<") || strings.HasPrefix(t, "#") {
				continue
			}
			r.prompt = firstLine(t, 60)
			return r
		}
	}
	return r
}

// firstLine is a text's first line, cut to n runes with an ellipsis.
func firstLine(s string, n int) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	runes := []rune(s)
	return strings.TrimSpace(string(runes[:n])) + "…"
}

// readCodexIndex is the thread names Codex keeps in session_index.jsonl,
// one line per naming, the latest for a thread winning.
func readCodexIndex(home string) map[string]string {
	names := map[string]string{}
	b, err := os.ReadFile(filepath.Join(home, "session_index.jsonl"))
	if err != nil {
		return names
	}
	for _, line := range strings.Split(string(b), "\n") {
		var e struct {
			ID   string `json:"id"`
			Name string `json:"thread_name"`
		}
		if json.Unmarshal([]byte(line), &e) == nil && e.ID != "" && e.Name != "" {
			names[e.ID] = e.Name
		}
	}
	return names
}

// codexAppThreads lists the threads started elsewhere, latest activity
// first, at most limit of them; live are thread ids the column's
// sessions already carry, left out.
func codexAppThreads(home string, live map[string]bool, now time.Time, limit int) []codexThread {
	out := []codexThread{}
	if home == "" {
		return out
	}
	type file struct {
		path  string
		mtime time.Time
	}
	var files []file
	filepath.WalkDir(filepath.Join(home, "sessions"), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if name := d.Name(); !strings.HasPrefix(name, "rollout-") || !strings.HasSuffix(name, ".jsonl") {
			return nil
		}
		if info, err := d.Info(); err == nil {
			files = append(files, file{p, info.ModTime()})
		}
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].mtime.After(files[j].mtime) })
	names := readCodexIndex(home)
	for _, f := range files {
		var r *codexRollout
		if v, ok := codexMetaCache.Load(f.path); ok {
			r = v.(*codexRollout)
		} else if r = readCodexRollout(f.path); r != nil {
			codexMetaCache.Store(f.path, r)
		}
		if r == nil || !r.user || codexOwnOriginator(r.originator) || live[r.id] {
			continue
		}
		title := names[r.id]
		if title == "" {
			title = r.prompt
		}
		out = append(out, codexThread{ID: r.id, Title: title, Origin: codexOrigin(r.originator), Cwd: r.cwd,
			Created: r.created, Activity: f.mtime.Unix(), Working: now.Sub(f.mtime) <= agentWorkingSeconds*time.Second})
		if len(out) == limit {
			break
		}
	}
	return out
}

// readAgentThreadID is the thread a Codex session carries, from its
// status file — the notify command's JSON, or noteCodexThread's stub —
// "" before the first turn of a fresh conversation.
func readAgentThreadID(statusFile string) string {
	b, err := os.ReadFile(statusFile)
	if err != nil {
		return ""
	}
	var h struct {
		ThreadID string `json:"thread-id"`
	}
	json.Unmarshal(b, &h)
	return h.ThreadID
}

// noteCodexThread marks a session as carrying a thread from the start —
// one resumed from the column or the API — so the column's thread rows
// leave it out and Archive is offered at once: a stub of the notify
// JSON the first turn overwrites (readAgentThreadID, readAgentResumable).
func (s *Server) noteCodexThread(a hostAgent, session, id string) {
	file := s.agentStatusFile(a, session)
	os.MkdirAll(filepath.Dir(file), 0o755)
	b, _ := json.Marshal(map[string]string{"thread-id": id})
	os.WriteFile(file, b, 0o644)
}

// agentThreads is the column's list of threads started elsewhere: Codex
// only, with the threads its sessions carry left out.
func (s *Server) agentThreads(a hostAgent, list []agentSession) []codexThread {
	if !a.notify {
		return nil
	}
	live := map[string]bool{}
	for _, l := range list {
		if id := readAgentThreadID(s.agentStatusFile(a, l.Name)); id != "" {
			live[id] = true
		}
	}
	return codexAppThreads(codexHome(), live, time.Now(), codexThreadsShown)
}
