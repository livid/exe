package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"time"

	"exe/internal/codex"
)

// Sign in with ChatGPT: the OAuth flow the Codex CLI uses. The daemon holds
// the PKCE verifier; the browser opens auth.openai.com and lands on
// localhost:1455. When that callback can't reach this process (daemon on
// another machine), the user pastes the redirect URL instead.

const codexFlowTTL = 10 * time.Minute

// codexRefreshMargin renews the access token this long before expiry so a
// long tool loop doesn't straddle the deadline.
const codexRefreshMargin = 2 * time.Minute

type codexFlow struct {
	state    string
	verifier string
	listener net.Listener
	expires  time.Time
}

func (s *Server) codexCredsPath() string { return filepath.Join(s.StateDir, "openai.json") }

// codexCreds returns the cached sign-in, loading the file on first use.
// Callers treat the result as read-only.
func (s *Server) codexCreds() *codex.Creds {
	s.codexMu.Lock()
	defer s.codexMu.Unlock()
	return s.codexCredsLocked()
}

func (s *Server) codexCredsLocked() *codex.Creds {
	if !s.codexLoaded {
		s.codexLoaded = true
		c, err := codex.LoadCreds(s.codexCredsPath())
		if err != nil {
			log.Printf("openai: load credentials: %v", err)
		}
		s.codexCache = c
	}
	return s.codexCache
}

func (s *Server) codexStore(c *codex.Creds) error {
	s.codexMu.Lock()
	defer s.codexMu.Unlock()
	if err := codex.SaveCreds(s.codexCredsPath(), c); err != nil {
		return err
	}
	s.codexCache, s.codexLoaded = c, true
	return nil
}

// codexToken returns a live access token, refreshing (and persisting the
// rotated pair) when the stored one is expired or about to be. force
// refreshes even a seemingly-live token — the retry path after a 401.
func (s *Server) codexToken(ctx context.Context, force bool) (*codex.Creds, error) {
	s.codexMu.Lock()
	defer s.codexMu.Unlock()
	c := s.codexCredsLocked()
	if c == nil {
		return nil, errors.New("not signed in to ChatGPT (Configuration → OpenAI)")
	}
	if !force && time.Now().UnixMilli() < c.Expires-codexRefreshMargin.Milliseconds() {
		return c, nil
	}
	fresh, err := codex.Refresh(ctx, c.RefreshToken)
	if err != nil {
		return nil, fmt.Errorf("refresh ChatGPT token: %w", err)
	}
	if err := codex.SaveCreds(s.codexCredsPath(), fresh); err != nil {
		return nil, err
	}
	s.codexCache = fresh
	return fresh, nil
}

// handleOpenAIStart begins a sign-in: it arms a fresh PKCE flow, tries to
// bind the localhost callback (works when the browser runs on this
// machine), and hands the authorize URL to the UI to open.
func (s *Server) handleOpenAIStart(w http.ResponseWriter, r *http.Request) {
	flow, err := codex.NewFlow()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	f := &codexFlow{state: flow.State, verifier: flow.Verifier, expires: time.Now().Add(codexFlowTTL)}

	s.codexMu.Lock()
	if old := s.codexFlow; old != nil && old.listener != nil {
		old.listener.Close()
	}
	s.codexFlow = f
	s.codexMu.Unlock()

	bound := false
	if ln, err := net.Listen("tcp", codex.CallbackAddr); err == nil {
		bound = true
		f.listener = ln
		go s.serveCodexCallback(f, ln)
	}
	writeJSON(w, http.StatusOK, map[string]any{"url": flow.URL, "callback": bound})
}

// serveCodexCallback accepts the one browser redirect for flow f and closes
// the listener; a second sign-in or the flow TTL also close it.
func (s *Server) serveCodexCallback(f *codexFlow, ln net.Listener) {
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != codex.CallbackPath {
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query()
		if q.Get("state") != f.state || q.Get("code") == "" {
			http.Error(w, "state mismatch or missing code — restart the sign-in from exe", http.StatusBadRequest)
			return
		}
		err := s.codexComplete(r.Context(), f, q.Get("code"))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			fmt.Fprintf(w, "<h3>exe: sign-in failed</h3><p>%s</p>", err)
			return
		}
		fmt.Fprint(w, "<h3>exe: ChatGPT sign-in complete.</h3><p>You can close this window.</p>")
		go ln.Close()
	})}
	go func() {
		time.Sleep(time.Until(f.expires))
		ln.Close()
	}()
	srv.Serve(ln)
}

// codexComplete exchanges the code and persists the resulting credentials;
// it consumes the flow so the paste path and the callback can't both win.
func (s *Server) codexComplete(ctx context.Context, f *codexFlow, code string) error {
	s.codexMu.Lock()
	if s.codexFlow != f {
		s.codexMu.Unlock()
		return errors.New("this sign-in was superseded — start again")
	}
	s.codexFlow = nil
	s.codexMu.Unlock()

	creds, err := codex.Exchange(ctx, code, f.verifier)
	if err != nil {
		return err
	}
	if err := s.codexStore(creds); err != nil {
		return err
	}
	s.chatStatMu.Lock()
	s.chatStatRes = nil
	s.chatStatMu.Unlock()
	log.Printf("openai: signed in as %s (%s plan)", creds.Email, creds.Plan)
	return nil
}

// handleOpenAIComplete finishes the flow from a pasted redirect URL (or
// bare authorization code) when the localhost callback couldn't fire.
func (s *Server) handleOpenAIComplete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Input string `json:"input"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.codexMu.Lock()
	f := s.codexFlow
	s.codexMu.Unlock()
	if f == nil || time.Now().After(f.expires) {
		writeErr(w, http.StatusConflict, errors.New("no sign-in in progress — click Sign in with ChatGPT first"))
		return
	}
	code, err := codex.ParseRedirectInput(req.Input, f.state)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.codexComplete(r.Context(), f, code); err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	if f.listener != nil {
		f.listener.Close()
	}
	c := s.codexCreds()
	writeJSON(w, http.StatusOK, map[string]any{"status": "authenticated", "email": c.Email, "plan": c.Plan})
}

// handleOpenAIStatus reports the sign-in and subscription state for the
// Configuration window's OpenAI tab.
func (s *Server) handleOpenAIStatus(w http.ResponseWriter, r *http.Request) {
	cfg := s.Config()
	res := map[string]any{
		"authenticated": false,
		"provider":      cfg.ChatProvider,
		"model":         cfg.OpenAI.Model,
	}
	s.codexMu.Lock()
	if f := s.codexFlow; f != nil && time.Now().Before(f.expires) {
		res["pending"] = true
	}
	s.codexMu.Unlock()
	if c := s.codexCreds(); c != nil {
		res["authenticated"] = true
		res["email"] = c.Email
		res["plan"] = c.Plan
		res["account_id"] = c.AccountID
		res["expires"] = c.Expires
	}
	writeJSON(w, http.StatusOK, res)
}

// codexUsage reads the subscription's rate-limit usage (the 5-hour and
// weekly windows, plus any credit balance) on a live token, trying once
// more on a freshly refreshed one when the backend rejects the token on
// file. Behind the Configuration window's OpenAI tab, the Chat window's
// status line and the Codex window's (agentstatus.go).
func (s *Server) codexUsage(ctx context.Context) (*codex.Usage, error) {
	c, err := s.codexToken(ctx, false)
	if err != nil {
		return nil, err
	}
	u, err := codex.FetchUsage(ctx, c.AccessToken, c.AccountID)
	if errors.Is(err, codex.ErrUnauthorized) {
		if c, err = s.codexToken(ctx, true); err == nil {
			u, err = codex.FetchUsage(ctx, c.AccessToken, c.AccountID)
		}
	}
	return u, err
}

// handleOpenAIUsage serves codexUsage: 409 without a sign-in to read on,
// 502 when the backend would not answer.
func (s *Server) handleOpenAIUsage(w http.ResponseWriter, r *http.Request) {
	if s.codexCreds() == nil {
		writeErr(w, http.StatusConflict, errors.New("not signed in to ChatGPT (Configuration → OpenAI)"))
		return
	}
	u, err := s.codexUsage(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

func (s *Server) handleOpenAILogout(w http.ResponseWriter, r *http.Request) {
	s.codexMu.Lock()
	if f := s.codexFlow; f != nil && f.listener != nil {
		f.listener.Close()
	}
	s.codexFlow = nil
	err := codex.DeleteCreds(s.codexCredsPath())
	s.codexCache, s.codexLoaded = nil, true
	s.codexMu.Unlock()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.chatStatMu.Lock()
	s.chatStatRes = nil
	s.chatStatMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]string{"status": "signed out"})
}
