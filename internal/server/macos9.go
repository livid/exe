package server

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"exe/internal/macos9"
	"exe/internal/vncpace"
	"github.com/coder/websocket"
)

func (s *Server) macOS9Manager() *macos9.Manager {
	s.macOS9Once.Do(func() { s.macOS9 = macos9.New(filepath.Join(s.StateDir, "mac-os9")) })
	return s.macOS9
}

// The Mac OS X app's Mac keeps its disk in a root of its own and runs the
// emulator the Mac OS 9 root holds.
func (s *Server) macOSXManager() *macos9.Manager {
	s.macOSXOnce.Do(func() {
		s.macOSX = macos9.NewMacOSX(filepath.Join(s.StateDir, "mac-osx"), filepath.Join(s.StateDir, "mac-os9"))
	})
	return s.macOSX
}

// mac is the Mac a request names: the two apps have the same API, one
// under /v1/macos9 and one under /v1/macosx.
func (s *Server) mac(r *http.Request) *macos9.Manager {
	if r.URL.Path == "/v1/macosx" || strings.HasPrefix(r.URL.Path, "/v1/macosx/") {
		return s.macOSXManager()
	}
	return s.macOS9Manager()
}
func macOS9SameOrigin(w http.ResponseWriter, r *http.Request) bool {
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		if err != nil || u.Host != r.Host || (u.Scheme != "http" && u.Scheme != "https") {
			http.Error(w, "origin not allowed", http.StatusForbidden)
			return false
		}
	}
	return true
}
func (s *Server) handleMacOS9Status(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	m := s.mac(r)
	st := m.Status()
	// The agent's way in, for a Mac with a shell (nil for Mac OS 9's); its
	// key is minted the first time the window asks.
	if a, err := m.Agent(); err == nil {
		st.Agent = a
	} else {
		log.Printf("mac agent: %v", err)
	}
	_ = json.NewEncoder(w).Encode(st)
}
func (s *Server) handleMacOS9Start(w http.ResponseWriter, r *http.Request) {
	if !macOS9SameOrigin(w, r) {
		return
	}
	if err := s.mac(r).Start(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.handleMacOS9Status(w, r)
}
func (s *Server) handleMacOS9Cancel(w http.ResponseWriter, r *http.Request) {
	if !macOS9SameOrigin(w, r) {
		return
	}
	s.mac(r).Cancel()
	s.handleMacOS9Status(w, r)
}
func (s *Server) handleMacOS9Finish(w http.ResponseWriter, r *http.Request) {
	if !macOS9SameOrigin(w, r) {
		return
	}
	if err := s.mac(r).Finish(); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	s.handleMacOS9Status(w, r)
}

// The viewer says what it found out about the Mac's pointer (see
// macos9.Manager.SetPointerTrim), and every viewer reads it in the status.
func (s *Server) handleMacOS9Pointer(w http.ResponseWriter, r *http.Request) {
	if !macOS9SameOrigin(w, r) {
		return
	}
	var body struct {
		Trim float64 `json:"trim"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "Invalid pointer request.", http.StatusBadRequest)
		return
	}
	if err := s.mac(r).SetPointerTrim(body.Trim); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	s.handleMacOS9Status(w, r)
}
func (s *Server) handleMacOS9Console(w http.ResponseWriter, r *http.Request) {
	if !macOS9SameOrigin(w, r) {
		return
	}
	tcp, err := s.mac(r).Console(r.Context())
	if err != nil {
		http.Error(w, "The Mac display is not running.", http.StatusServiceUnavailable)
		return
	}
	defer tcp.Close()
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"binary"}, CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	defer ws.CloseNow()
	ws.SetReadLimit(4 << 20)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	conn := websocket.NetConn(ctx, ws, websocket.MessageBinary)
	// The relay paces the screen updates: it keeps a request waiting at the emulator, next to
	// it, and sends on what the viewer asked for, so a network round trip does not hold up
	// each frame (internal/vncpace).
	done := make(chan struct{})
	go func() { _ = vncpace.Relay(conn, tcp); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
	}
	cancel()
	_ = tcp.Close()
	_ = ws.CloseNow()
	<-done
}

func (s *Server) handleMacOS9CD(w http.ResponseWriter, r *http.Request) {
	cd, err := s.mac(r).CD(r.Context())
	macOS9CDResponse(w, cd, err)
}
func macOS9CDResponse(w http.ResponseWriter, cd macos9.CDStatus, err error) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{"message": err.Error(), "locked": errors.Is(err, macos9.ErrCDLocked)})
		return
	}
	_ = json.NewEncoder(w).Encode(cd)
}
func (s *Server) handleMacOS9ChangeCD(w http.ResponseWriter, r *http.Request) {
	if !macOS9SameOrigin(w, r) {
		return
	}
	var body struct {
		Filename string `json:"filename"`
		Force    bool   `json:"force"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		http.Error(w, "Invalid CD request.", http.StatusBadRequest)
		return
	}
	cd, err := s.mac(r).ChangeCD(r.Context(), body.Filename, body.Force)
	macOS9CDResponse(w, cd, err)
}
func (s *Server) handleMacOS9UploadCD(w http.ResponseWriter, r *http.Request) {
	if !macOS9SameOrigin(w, r) {
		return
	}
	mac := s.mac(r)
	r.Body = http.MaxBytesReader(w, r.Body, mac.MaxCDSize())
	// A raw request body streams directly to disk; the browser supplies the
	// filename separately, never a host filesystem path.
	image, err := mac.ImportCD(r.URL.Query().Get("filename"), r.Body)
	if err != nil {
		var large *http.MaxBytesError
		code := http.StatusBadRequest
		if errors.As(err, &large) {
			code = http.StatusRequestEntityTooLarge
		}
		http.Error(w, err.Error(), code)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(image)
}
