package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path/filepath"

	"exe/internal/macos9"
	"github.com/coder/websocket"
)

func (s *Server) macOS9Manager() *macos9.Manager {
	s.macOS9Once.Do(func() { s.macOS9 = macos9.New(filepath.Join(s.StateDir, "mac-os9")) })
	return s.macOS9
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
	_ = json.NewEncoder(w).Encode(s.macOS9Manager().Status())
}
func (s *Server) handleMacOS9Start(w http.ResponseWriter, r *http.Request) {
	if !macOS9SameOrigin(w, r) {
		return
	}
	if err := s.macOS9Manager().Start(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.handleMacOS9Status(w, r)
}
func (s *Server) handleMacOS9Cancel(w http.ResponseWriter, r *http.Request) {
	if !macOS9SameOrigin(w, r) {
		return
	}
	s.macOS9Manager().Cancel()
	s.handleMacOS9Status(w, r)
}
func (s *Server) handleMacOS9Finish(w http.ResponseWriter, r *http.Request) {
	if !macOS9SameOrigin(w, r) {
		return
	}
	if err := s.macOS9Manager().Finish(); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	s.handleMacOS9Status(w, r)
}
func (s *Server) handleMacOS9Console(w http.ResponseWriter, r *http.Request) {
	if !macOS9SameOrigin(w, r) {
		return
	}
	tcp, err := s.macOS9Manager().Console(r.Context())
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
	done := make(chan struct{}, 1)
	go func() { _, _ = io.Copy(tcp, conn); done <- struct{}{} }()
	go func() { _, _ = io.Copy(conn, tcp); done <- struct{}{} }()
	<-done
	cancel()
	_ = tcp.Close()
	_ = ws.CloseNow()
	<-done
}

func (s *Server) handleMacOS9CD(w http.ResponseWriter, r *http.Request) {
	cd, err := s.macOS9Manager().CD(r.Context())
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
	cd, err := s.macOS9Manager().ChangeCD(r.Context(), body.Filename, body.Force)
	macOS9CDResponse(w, cd, err)
}
func (s *Server) handleMacOS9UploadCD(w http.ResponseWriter, r *http.Request) {
	if !macOS9SameOrigin(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, macos9.MaxCDSize)
	// A raw request body streams directly to disk; the browser supplies the
	// filename separately, never a host filesystem path.
	image, err := s.macOS9Manager().ImportCD(r.URL.Query().Get("filename"), r.Body)
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
