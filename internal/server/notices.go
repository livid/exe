package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Notices are the desk's toasts that stay. A script on this machine puts
// one up (the hub watcher does when a usage limit stops Claude's turns:
// there the toast is the nudge, not a reply in a public thread), every open
// desk shows it at once, and it stays on every desk, over reloads and
// restarts, until someone closes it there. A notice is said again under
// its id: a POST with an id already up changes it in place, so a script
// keeps one toast current instead of stacking them.
//
// The list lives in notices.json in the state folder; every change goes
// to open desks and phones over /v1/apps/events, the stream both keep open,
// as {"app": "@notices", "notices": [...]}, the whole list.
type notice struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Body  string `json:"body,omitempty"`
	Time  int64  `json:"time"` // unix ms, when it was last said
}

const (
	noticeKeep     = 8 // most up at once: the oldest goes
	noticeTitleMax = 120
	noticeBodyMax  = 1000
)

var noticeID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

func (s *Server) noticesPath() string { return filepath.Join(s.StateDir, "notices.json") }

// noticesLocked reads the list; callers hold s.noticeMu.
func (s *Server) noticesLocked() []notice {
	var list []notice
	if b, err := os.ReadFile(s.noticesPath()); err == nil {
		json.Unmarshal(b, &list)
	}
	if list == nil {
		list = []notice{}
	}
	return list
}

// saveNoticesLocked writes the list and tells every open desk; callers hold s.noticeMu.
func (s *Server) saveNoticesLocked(list []notice) error {
	b, err := json.Marshal(list)
	if err != nil {
		return err
	}
	if err := os.WriteFile(s.noticesPath(), append(b, '\n'), 0o600); err != nil {
		return err
	}
	s.broadcastAppEvent(map[string]any{"app": "@notices", "notices": list})
	return nil
}

func (s *Server) handleNoticesGet(w http.ResponseWriter, r *http.Request) {
	s.noticeMu.Lock()
	list := s.noticesLocked()
	s.noticeMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"notices": list})
}

func (s *Server) handleNoticePost(w http.ResponseWriter, r *http.Request) {
	if tsVia(r) != "local" {
		writeErr(w, http.StatusForbidden, errors.New("notices take callers on this machine only"))
		return
	}
	var n notice
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&n); err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("bad request body"))
		return
	}
	n.Title, n.Body = strings.TrimSpace(n.Title), strings.TrimSpace(n.Body)
	if n.Title == "" {
		writeErr(w, http.StatusBadRequest, errors.New("a notice needs a title"))
		return
	}
	if len(n.Title) > noticeTitleMax || len(n.Body) > noticeBodyMax {
		writeErr(w, http.StatusBadRequest, errors.New("title or body too long"))
		return
	}
	if n.ID == "" {
		b := make([]byte, 8)
		rand.Read(b)
		n.ID = hex.EncodeToString(b)
	} else if !noticeID.MatchString(n.ID) {
		writeErr(w, http.StatusBadRequest, errors.New("id is lower-case letters, digits and dashes"))
		return
	}
	n.Time = time.Now().UnixMilli()
	s.noticeMu.Lock()
	defer s.noticeMu.Unlock()
	out := s.noticesLocked()
	at := -1
	for i, o := range out {
		if o.ID == n.ID {
			at = i
		}
	}
	if at >= 0 {
		out[at] = n // said again: in its place in the stack
	} else {
		out = append(out, n)
	}
	if len(out) > noticeKeep {
		out = out[len(out)-noticeKeep:]
	}
	if err := s.saveNoticesLocked(out); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": n.ID})
}

// handleNoticeDelete is a desk's close box: gone from every desk.
func (s *Server) handleNoticeDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.noticeMu.Lock()
	defer s.noticeMu.Unlock()
	list := s.noticesLocked()
	out := make([]notice, 0, len(list))
	for _, o := range list {
		if o.ID != id {
			out = append(out, o)
		}
	}
	if len(out) == len(list) {
		writeErr(w, http.StatusNotFound, errors.New("no such notice"))
		return
	}
	if err := s.saveNoticesLocked(out); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"notices": out})
}
