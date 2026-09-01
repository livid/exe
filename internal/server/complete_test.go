package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"exe/internal/config"
)

// A completion streams the model's fragments as NDJSON and ends with a done
// line; the backend sees the system prompt ahead of the user prompt, the
// configured model, thinking switched off and the options the caller sent.
func TestChatComplete(t *testing.T) {
	var seen map[string]any
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&seen)
		fmt.Fprintln(w, `{"message":{"role":"assistant","content":"Hello, "},"done":false}`)
		fmt.Fprintln(w, `{"message":{"role":"assistant","content":"world."},"done":true}`)
	}))
	defer backend.Close()

	s := New(&config.Config{Ollama: config.OllamaConfig{BaseURL: backend.URL, Model: "m"}}, nil, nil, "", t.TempDir())
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/v1/chat/complete", "application/json",
		strings.NewReader(`{"system":"fix it","prompt":"helo wrold","effort":"off","options":{"temperature":0}}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/x-ndjson" {
		t.Fatalf("content-type = %q", ct)
	}
	var lines []map[string]any
	dec := json.NewDecoder(resp.Body)
	for dec.More() {
		var m map[string]any
		if err := dec.Decode(&m); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, m)
	}
	if len(lines) != 3 || lines[0]["delta"] != "Hello, " || lines[1]["delta"] != "world." ||
		lines[2]["done"] != true || lines[2]["model"] != "m" {
		t.Fatalf("lines = %v", lines)
	}

	if seen["model"] != "m" {
		t.Fatalf("backend model = %v", seen["model"])
	}
	if seen["think"] != false {
		t.Fatalf("backend think = %v, want false", seen["think"])
	}
	if opts, _ := seen["options"].(map[string]any); opts["temperature"] != 0.0 {
		t.Fatalf("backend options = %v, want temperature 0", seen["options"])
	}
	msgs, _ := seen["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("backend messages = %v", msgs)
	}
	sys, _ := msgs[0].(map[string]any)
	usr, _ := msgs[1].(map[string]any)
	if sys["role"] != "system" || sys["content"] != "fix it" || usr["role"] != "user" || usr["content"] != "helo wrold" {
		t.Fatalf("backend messages = %v", msgs)
	}
}

// Failures that happen before any fragment streams are plain JSON errors,
// so an app can tell "nothing configured" from "the model choked".
func TestChatCompleteErrors(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"model not found"}`, http.StatusNotFound)
	}))
	defer backend.Close()

	post := func(s *Server, body string) (int, string) {
		ts := httptest.NewServer(s.Handler())
		defer ts.Close()
		resp, err := http.Post(ts.URL+"/v1/chat/complete", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out struct{ Error string }
		json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out.Error
	}

	s := New(&config.Config{Ollama: config.OllamaConfig{BaseURL: backend.URL, Model: "m"}}, nil, nil, "", t.TempDir())
	if code, msg := post(s, `{"prompt":"   "}`); code != http.StatusBadRequest || msg == "" {
		t.Fatalf("empty prompt: %d %q", code, msg)
	}
	if code, msg := post(s, `{"prompt":"hi"}`); code != http.StatusBadGateway || !strings.Contains(msg, "model not found") {
		t.Fatalf("backend failure: %d %q", code, msg)
	}
	if code, msg := post(s, `{"prompt":"`+strings.Repeat("x", completeMaxPrompt+1)+`"}`); code != http.StatusRequestEntityTooLarge || msg == "" {
		t.Fatalf("oversize prompt: %d %q", code, msg)
	}

	unset := New(&config.Config{}, nil, nil, "", t.TempDir())
	if code, msg := post(unset, `{"prompt":"hi"}`); code != http.StatusServiceUnavailable || !strings.Contains(msg, "base_url") {
		t.Fatalf("unconfigured: %d %q", code, msg)
	}
}
