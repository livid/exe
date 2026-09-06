package server

import (
	"context"
	"encoding/json"
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
