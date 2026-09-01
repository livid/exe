// One-shot model completions for desktop apps.
//
// POST /v1/chat/complete asks the configured Ollama model one question and
// streams the answer back: no session, no tools, no history. The app holds
// the prompt; the daemon holds the endpoint and the key, so a page in the
// browser never sees either. Blue Pencil's grammar pass runs on it, and any
// app can. The ChatGPT provider is deliberately not consulted — this is the
// Ollama endpoint, the one the user pointed exe at for local work.
package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"exe/internal/agent"
)

const (
	completeMaxPrompt = 64 << 10
	completeMaxSystem = 8 << 10
)

// handleChatComplete streams newline-delimited JSON: {"delta":"…"} per
// content fragment, then {"done":true,"model":"…"}. The body carries the
// prompts plus optional model, effort and Ollama options overrides. A
// failure before the first fragment is a plain JSON error (502/503); one
// mid-stream lands as a final {"error":"…"} line. Closing the request
// cancels the model call.
func (s *Server) handleChatComplete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		System string `json:"system"`
		Prompt string `json:"prompt"`
		Model  string `json:"model"`
		Effort string `json:"effort"`
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
	cfg := s.Config()
	if cfg.Ollama.BaseURL == "" {
		writeErr(w, http.StatusServiceUnavailable, errors.New("ollama.base_url is not configured"))
		return
	}
	acfg := agent.Config{BaseURL: cfg.Ollama.BaseURL, APIKey: cfg.Ollama.APIKey,
		Model: cfg.Ollama.Model, Effort: cfg.Ollama.Effort, Options: req.Options}
	if m := strings.TrimSpace(req.Model); m != "" {
		acfg.Model = m
	}
	if e := strings.ToLower(strings.TrimSpace(req.Effort)); e != "" {
		acfg.Effort = e
	}
	if acfg.Model == "" {
		writeErr(w, http.StatusServiceUnavailable, errors.New("ollama.model is not configured"))
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
	_, err := agent.ChatStream(r.Context(), acfg, msgs, nil, func(delta string) {
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
	emit(map[string]any{"done": true, "model": acfg.Model})
}
