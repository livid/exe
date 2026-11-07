package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync"
)

// The folders an agent has worked in on this machine, the latest first:
// what the New Session dialog of the agent's window offers, beside a
// field for any other. Each agent's own records say where its sessions
// ran — Claude Code's transcripts (claudeDirs), Codex's rollouts
// (codexDirs) — and only the sessions a person worked in count: headless
// runs, sub-agents and the hub watchers' turns are passed over, as the
// column passes them over. A folder that is no longer on this machine is
// left out, as a session could not start there. The window's default
// folder (agentProjectDir) is always on the list, last with no time when
// the agent has not worked there yet.
//
//	GET /v1/agents/{app}/dirs             {"dirs":[{path, at}], "default": path}: at most agentDirsShown
//	GET /v1/agents/{app}/dirs?path=<dir>  {"path": dir made whole}, or 400 with why a session cannot start there
//	                                      (agentSessionDir: "~" is the home folder)

// agentDirsShown caps the list: the dialog shows fourteen rows and
// scrolls the rest.
const agentDirsShown = 30

type agentDir struct {
	Path string `json:"path"`
	At   int64  `json:"at"` // the latest write of a session's record there, unix seconds; 0 for the default folder unused
}

// agentDirList gathers the folders from records the latest first: each
// record's folder, "" for one a person did not work in, and the time of
// its last write. A folder is placed by its latest record, listed once,
// and left out when it is gone; the list stops at limit.
type agentDirList struct {
	out   []agentDir
	seen  map[string]bool
	limit int
}

func newAgentDirList(limit int) *agentDirList {
	return &agentDirList{out: []agentDir{}, seen: map[string]bool{}, limit: limit}
}

// add takes the next record's folder; false once the list is full.
func (l *agentDirList) add(cwd string, at int64) bool {
	if !filepath.IsAbs(cwd) {
		return true
	}
	dir := filepath.Clean(cwd)
	if l.seen[dir] {
		return true
	}
	l.seen[dir] = true // a folder that is gone stays gone for its older records too
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return true
	}
	l.out = append(l.out, agentDir{Path: dir, At: at})
	return len(l.out) != l.limit
}

// claudeHead is what a transcript's first user entry says about where and
// how the session ran; it never changes once written.
type claudeHead struct{ cwd, entrypoint string }

var claudeHeadCache sync.Map // path → claudeHead

// claudeHeadOf reads a transcript as far as its first user entry; ok is
// false for one that has none yet, which is read again next time. A
// transcript the column has read already is not opened again.
func claudeHeadOf(path string) (claudeHead, bool) {
	if v, ok := claudeHeadCache.Load(path); ok {
		return v.(claudeHead), true
	}
	if v, ok := claudeMetaCache.Load(path); ok {
		t := v.(*claudeTranscript)
		h := claudeHead{t.cwd, t.entrypoint}
		claudeHeadCache.Store(path, h)
		return h, true
	}
	f, err := os.Open(path)
	if err != nil {
		return claudeHead{}, false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if !bytes.Contains(line, []byte(`"type":"user"`)) {
			continue
		}
		var u struct {
			Type       string `json:"type"`
			Entrypoint string `json:"entrypoint"`
			Cwd        string `json:"cwd"`
			Message    struct {
				Role string `json:"role"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &u) != nil || u.Type != "user" || u.Message.Role != "user" {
			continue
		}
		h := claudeHead{u.Cwd, u.Entrypoint}
		claudeHeadCache.Store(path, h)
		return h, true
	}
	return claudeHead{}, false
}

// claudeDirs lists Claude Code's folders. Its transcripts are kept under
// <home>/projects/<folder>/, the folder named after the directory the
// session ran in with every "/" and "." made a dash — a name that cannot
// be turned back into the path — so the path is read out of a
// transcript's first user entry, its "cwd". The transcripts come latest
// first (claudeTranscriptFiles), so a folder is settled by its latest
// interactive one and its older ones are never opened; a folder of
// headless runs alone (`claude -p`, the SDK: the painters, the hub
// watcher) has each read once, then cached.
func claudeDirs(home string, limit int) []agentDir {
	l := newAgentDirList(limit)
	if home == "" {
		return l.out
	}
	settled := map[string]bool{} // project folders whose place is known
	for _, f := range claudeTranscriptFiles(home) {
		folder := filepath.Dir(f.path)
		if settled[folder] {
			continue
		}
		h, ok := claudeHeadOf(f.path)
		if !ok || claudeHeadless(h.entrypoint) || !filepath.IsAbs(h.cwd) {
			continue
		}
		settled[folder] = true
		if !l.add(h.cwd, f.mtime.Unix()) {
			break
		}
	}
	return l.out
}

// codexWorkedIn says whether a person worked in a Codex thread: one of
// the user's own (not a sub-agent's), run in the CLI's own screen —
// source "cli": the window's sessions, codex in a Terminal — or started
// from the ChatGPT app, the Codex app or VS Code. Not a `codex exec`
// run, nor a thread the CLI's app-server runs for a program (the hub
// watcher's, originator codex-tui or codex_claude_watch, source vscode).
func codexWorkedIn(r *codexRollout) bool {
	if r == nil || !r.user {
		return false
	}
	if r.source == "cli" {
		return true
	}
	return r.source != "exec" && !codexOwnOriginator(r.originator) && codexOrigin(r.originator) != r.originator
}

// codexDirs lists Codex's folders: each thread's rollout names its folder
// on its first line (session_meta's cwd), in the live sessions and the
// archived ones alike. Every rollout is read once, then cached
// (codexRolloutOf), so a later list only walks the folders.
func codexDirs(home string, limit int) []agentDir {
	l := newAgentDirList(limit)
	if home == "" {
		return l.out
	}
	files := codexRolloutFiles(filepath.Join(home, "sessions"), filepath.Join(home, "archived_sessions"))
	for _, f := range files {
		if r := codexRolloutOf(f.path); codexWorkedIn(r) && !l.add(r.cwd, f.mtime.Unix()) {
			break
		}
	}
	return l.out
}

func (s *Server) handleAgentDirs(w http.ResponseWriter, r *http.Request) {
	a, ok := s.agentOf(w, r)
	if !ok {
		return
	}
	if r.URL.Query().Has("path") {
		dir, err := agentSessionDir(r.URL.Query().Get("path"))
		if err == nil && dir == "" {
			err = errors.New("type a folder's path")
		}
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"path": dir})
		return
	}
	var dirs []agentDir
	if a.notify {
		dirs = codexDirs(codexHome(), agentDirsShown)
	} else {
		dirs = claudeDirs(claudeHome(), agentDirsShown)
	}
	def := s.agentProjectDir()
	listed := false
	for _, d := range dirs {
		listed = listed || d.Path == def
	}
	if !listed && def != "" {
		dirs = append(dirs, agentDir{Path: def})
	}
	writeJSON(w, http.StatusOK, map[string]any{"dirs": dirs, "default": def})
}
