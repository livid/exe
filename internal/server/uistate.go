package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// uiState holds the web UI's shared window layout — positions, sizes, which
// windows are open. The daemon treats it as an opaque JSON blob: a browser
// PUTs the whole snapshot, every other connected browser receives it over
// the /v1/ui/events SSE stream, and it is persisted to disk so the desktop
// comes back the way it was left across restarts.
type uiState struct {
	mu     sync.Mutex
	loaded bool
	rev    int64
	data   json.RawMessage
	subs   map[chan []byte]struct{}
}

// uiStateMax caps the layout blob; it is a few hundred bytes in practice.
const uiStateMax = 64 << 10

func (s *Server) uiStatePath() string { return filepath.Join(s.StateDir, "ui-state.json") }

// loadLocked reads the persisted state once; callers hold u.mu.
func (u *uiState) loadLocked(path string) {
	if u.loaded {
		return
	}
	u.loaded = true
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var f struct {
		Rev   int64           `json:"rev"`
		State json.RawMessage `json:"state"`
	}
	if json.Unmarshal(b, &f) == nil && len(f.State) > 0 {
		u.rev, u.data = f.Rev, f.State
	}
}

func (u *uiState) eventLocked(client string) []byte {
	data := u.data
	if data == nil {
		data = json.RawMessage("null")
	}
	// build rides along so a desktop reconnecting after a deploy learns at
	// once that the daemon ships a newer page than the one it is running
	ev, _ := json.Marshal(map[string]any{"rev": u.rev, "client": client, "state": data, "build": deskBuild})
	return ev
}

func (s *Server) handleUIStateGet(w http.ResponseWriter, r *http.Request) {
	s.ui.mu.Lock()
	s.ui.loadLocked(s.uiStatePath())
	rev, data := s.ui.rev, s.ui.data
	s.ui.mu.Unlock()
	if data == nil {
		data = json.RawMessage("null")
	}
	writeJSON(w, http.StatusOK, map[string]any{"rev": rev, "state": data})
}

func (s *Server) handleUIStatePut(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Client string          `json:"client"`
		State  json.RawMessage `json:"state"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, uiStateMax+1)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if len(req.State) == 0 || len(req.State) > uiStateMax {
		writeErr(w, http.StatusBadRequest, errors.New("state missing or too large"))
		return
	}
	s.ui.mu.Lock()
	s.ui.loadLocked(s.uiStatePath())
	s.ui.rev++
	s.ui.data = append(json.RawMessage(nil), req.State...)
	rev := s.ui.rev
	persist, _ := json.Marshal(map[string]any{"rev": rev, "state": s.ui.data})
	ev := s.ui.eventLocked(req.Client)
	for ch := range s.ui.subs {
		select {
		case ch <- ev:
		default: // slow subscriber: every event is a full snapshot, the next one catches it up
		}
	}
	s.ui.mu.Unlock()
	os.WriteFile(s.uiStatePath(), append(persist, '\n'), 0o600)
	writeJSON(w, http.StatusOK, map[string]any{"rev": rev})
}

// uiAnnounce sends every connected browser an event that is not a
// layout — {"term":…} when a Terminal session starts (termsessions.go),
// so every desk opens its window. The snapshot and its rev stay as they
// are: a browser tells the event from a layout by its missing "state".
func (s *Server) uiAnnounce(ev any) {
	b, err := json.Marshal(ev)
	if err != nil {
		return
	}
	s.ui.mu.Lock()
	defer s.ui.mu.Unlock()
	for ch := range s.ui.subs {
		select {
		case ch <- b:
		default: // a slow subscriber: its stream's reconnect, or its next load, opens the window
		}
	}
}

// handleUIStateEvents is an SSE stream of layout snapshots: the current one
// on connect, then one per change (tagged with the originating client so a
// browser can ignore its own echoes).
func (s *Server) handleUIStateEvents(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, errors.New("streaming unsupported"))
		return
	}
	ch := make(chan []byte, 8)
	s.ui.mu.Lock()
	s.ui.loadLocked(s.uiStatePath())
	if s.ui.subs == nil {
		s.ui.subs = make(map[chan []byte]struct{})
	}
	s.ui.subs[ch] = struct{}{}
	first := s.ui.eventLocked("")
	s.ui.mu.Unlock()
	defer func() {
		s.ui.mu.Lock()
		delete(s.ui.subs, ch)
		s.ui.mu.Unlock()
	}()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "data: %s\n\n", first)
	fl.Flush()
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev := <-ch:
			fmt.Fprintf(w, "data: %s\n\n", ev)
			fl.Flush()
		case <-ping.C: // comment line keeps idle proxies from timing the stream out
			fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
		}
	}
}
