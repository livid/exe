// One-shot model completions for desktop apps.
//
// POST /v1/chat/complete asks a model one question and streams the answer
// back: no session, no tools, no history. The app holds the prompt; the
// daemon holds the endpoint and the key, so a page in the browser never
// sees either. Blue Pencil's grammar pass runs on it, and any app can. The
// call runs on the Ollama endpoint — the one the user pointed exe at for
// local work — unless the body says "provider": "openai", which runs it on
// the ChatGPT subscription signed in under Configuration → OpenAI, the way
// the Chat window does.
package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"exe/internal/agent"
	"exe/internal/codex"
)

const (
	completeMaxPrompt = 64 << 10
	completeMaxSystem = 8 << 10
)

// handleChatComplete streams newline-delimited JSON: {"delta":"…"} per
// content fragment, then {"done":true,"model":"…","provider":"…"}. The
// body carries the prompts plus optional provider, model, effort and
// Ollama options overrides. A failure before the first fragment is a plain
// JSON error (400/502/503); one mid-stream lands as a final {"error":"…"}
// line. Closing the request cancels the model call.
func (s *Server) handleChatComplete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		// Provider is "ollama" (the default) or "openai".
		Provider string `json:"provider"`
		System   string `json:"system"`
		Prompt   string `json:"prompt"`
		Model    string `json:"model"`
		Effort   string `json:"effort"`
		// Options go to Ollama as-is: temperature, seed, num_predict…
		Options map[string]any `json:"options"`
	}
	body := io.LimitReader(r.Body, completeMaxPrompt+completeMaxSystem+4096)
	if err := json.NewDecoder(body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(req.Prompt) == "" {
		writeErr(w, http.StatusBadRequest, errors.New("prompt is empty"))
		return
	}
	if len(req.Prompt) > completeMaxPrompt || len(req.System) > completeMaxSystem {
		writeErr(w, http.StatusRequestEntityTooLarge,
			fmt.Errorf("prompt over %d bytes", completeMaxPrompt))
		return
	}
	provider := strings.ToLower(strings.TrimSpace(req.Provider))
	if provider == "" {
		provider = "ollama"
	}
	model := strings.TrimSpace(req.Model)
	effort := strings.ToLower(strings.TrimSpace(req.Effort))
	cfg := s.Config()

	// call runs the one model turn on the chosen backend; it is built before
	// any byte is written so a backend not ready to run is a plain error.
	var call func(ctx context.Context, msgs []agent.Message, onDelta func(string)) error
	switch provider {
	case "ollama":
		if cfg.Ollama.BaseURL == "" {
			writeErr(w, http.StatusServiceUnavailable, errors.New("ollama.base_url is not configured"))
			return
		}
		acfg := agent.Config{BaseURL: cfg.Ollama.BaseURL, APIKey: cfg.Ollama.APIKey,
			Model: cfg.Ollama.Model, Effort: cfg.Ollama.Effort, Options: req.Options}
		if model != "" {
			acfg.Model = model
		}
		if effort != "" {
			acfg.Effort = effort
		}
		if acfg.Model == "" {
			writeErr(w, http.StatusServiceUnavailable, errors.New("ollama.model is not configured"))
			return
		}
		model = acfg.Model
		call = func(ctx context.Context, msgs []agent.Message, onDelta func(string)) error {
			_, err := agent.ChatStream(ctx, acfg, msgs, nil, onDelta)
			return err
		}
	case "openai":
		if s.codexCreds() == nil {
			writeErr(w, http.StatusServiceUnavailable, errors.New("not signed in to ChatGPT (Configuration → OpenAI)"))
			return
		}
		if model == "" {
			model = cfg.OpenAI.Model
		}
		if effort == "" {
			effort = cfg.OpenAI.Effort
		}
		if model == "" {
			writeErr(w, http.StatusServiceUnavailable, errors.New("openai.model is not configured"))
			return
		}
		// The ChatGPT path resolves (and auto-refreshes) the token per call
		// and retries once after a 401 in case the token was revoked out
		// from under us — a 401 comes before any fragment, so nothing has
		// streamed yet. The cache key names the system prompt: every call
		// an app makes with the same instructions lands on the same cache.
		ccfg := codex.ClientConfig{Model: model, Effort: effort, SessionKey: completeCacheKey(req.System)}
		call = func(ctx context.Context, msgs []agent.Message, onDelta func(string)) error {
			creds, err := s.codexToken(ctx, false)
			if err != nil {
				return err
			}
			ccfg.AccessToken, ccfg.AccountID = creds.AccessToken, creds.AccountID
			_, err = codex.ChatStream(ctx, ccfg, msgs, nil, onDelta)
			if errors.Is(err, codex.ErrUnauthorized) {
				if creds, err = s.codexToken(ctx, true); err != nil {
					return err
				}
				ccfg.AccessToken, ccfg.AccountID = creds.AccessToken, creds.AccountID
				_, err = codex.ChatStream(ctx, ccfg, msgs, nil, onDelta)
			}
			return err
		}
	default:
		writeErr(w, http.StatusBadRequest, fmt.Errorf("unknown provider %q", req.Provider))
		return
	}
	var msgs []agent.Message
	if req.System != "" {
		msgs = append(msgs, agent.Message{Role: "system", Content: req.System})
	}
	msgs = append(msgs, agent.Message{Role: "user", Content: req.Prompt})

	fl, _ := w.(http.Flusher)
	enc := json.NewEncoder(w)
	started := false
	emit := func(v any) {
		if !started {
			started = true
			w.Header().Set("Content-Type", "application/x-ndjson")
			w.Header().Set("X-Accel-Buffering", "no")
			w.WriteHeader(http.StatusOK)
		}
		enc.Encode(v)
		if fl != nil {
			fl.Flush()
		}
	}
	err := call(r.Context(), msgs, func(delta string) {
		emit(map[string]string{"delta": delta})
	})
	if err != nil {
		if !started {
			writeErr(w, http.StatusBadGateway, err)
			return
		}
		emit(map[string]string{"error": err.Error()})
		return
	}
	emit(map[string]any{"done": true, "model": model, "provider": provider})
}

// completeCacheKey is the ChatGPT prompt cache key for one-shot calls: a
// digest of the system prompt, so an app's calls share a cache without
// the app naming one.
func completeCacheKey(system string) string {
	sum := sha256.Sum256([]byte(system))
	return "complete-" + hex.EncodeToString(sum[:8])
}
