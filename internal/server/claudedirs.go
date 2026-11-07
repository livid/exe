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

// The folders Claude Code has worked in on this machine, the latest
// first: what the Claude Code window's New Session dialog offers, beside
// a field for any other. Claude Code keeps a session's transcript under
// <home>/projects/<folder>/, the folder named after the directory the
// session ran in with every "/" and "." made a dash — a name that cannot
// be turned back into the path — so the path is read out of a
// transcript's first user entry, its "cwd". A folder's time is the latest
// write of a transcript there that a person worked in: headless runs
// (`claude -p`, the SDK: the painters, the hub watcher) are passed over,
// as the column passes them over (claudeHeadless). A folder that is no
// longer on this machine is left out, as a session could not start there.
// The window's default folder (agentProjectDir) is always on the list,
// last with no time when Claude Code has not worked there yet.
//
//	GET /v1/agents/{app}/dirs             {"dirs":[{path, at}], "default": path}: at most claudeDirsShown, Claude Code only
//	GET /v1/agents/{app}/dirs?path=<dir>  {"path": dir made whole}, or 400 with why a session cannot start there
//	                                      (agentSessionDir: "~" is the home folder)

// claudeDirsShown caps the list: the dialog shows fourteen rows and
// scrolls the rest.
const claudeDirsShown = 30

type claudeDir struct {
	Path string `json:"path"`
	At   int64  `json:"at"` // the latest write of a transcript there, unix seconds; 0 for the default folder unused
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

// claudeDirs lists the folders, the latest first, at most limit of them.
// The transcripts come latest first (claudeTranscriptFiles), so a folder
// is settled by its latest interactive one and its older ones are never
// opened; a folder of headless runs alone has each read once, then cached.
func claudeDirs(home string, limit int) []claudeDir {
	out := []claudeDir{}
	if home == "" {
		return out
	}
	settled := map[string]bool{} // project folders whose place is known, or that are gone
	seen := map[string]bool{}    // paths listed
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
		dir := filepath.Clean(h.cwd)
		if seen[dir] {
			continue
		}
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			continue
		}
		seen[dir] = true
		out = append(out, claudeDir{Path: dir, At: f.mtime.Unix()})
		if len(out) == limit {
			break
		}
	}
	return out
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
	if a.notify {
		writeErr(w, http.StatusNotFound, errors.New("no recent folders for "+a.title+" yet"))
		return
	}
	def := s.agentProjectDir()
	dirs := claudeDirs(claudeHome(), claudeDirsShown)
	listed := false
	for _, d := range dirs {
		listed = listed || d.Path == def
	}
	if !listed && def != "" {
		dirs = append(dirs, claudeDir{Path: def})
	}
	writeJSON(w, http.StatusOK, map[string]any{"dirs": dirs, "default": def})
}
