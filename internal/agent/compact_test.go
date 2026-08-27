package agent

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func toolMsg(name, out string) Message {
	return Message{Role: "tool", ToolName: name, Content: out}
}
func asstCall(name string, size int) Message {
	m := Message{Role: "assistant"}
	tc := ToolCall{}
	tc.Function.Name = name
	tc.Function.Arguments = []byte(strings.Repeat("x", size))
	m.ToolCalls = []ToolCall{tc}
	return m
}

// The cut must land on a user or assistant message — a tail starting with
// a tool result would orphan it from its call — and leave roughly keep
// bytes verbatim.
func TestCompactCut(t *testing.T) {
	conv := []Message{
		{Role: "user", Content: strings.Repeat("a", 1000)},
		asstCall("bash", 100),
		toolMsg("bash", strings.Repeat("b", 1000)),
		asstCall("bash", 100),
		toolMsg("bash", strings.Repeat("c", 1000)),
		{Role: "assistant", Content: strings.Repeat("d", 500)},
		{Role: "user", Content: strings.Repeat("e", 500)},
		asstCall("bash", 100),
		toolMsg("bash", strings.Repeat("f", 1000)),
	}
	cut := CompactCut(conv, 2000)
	if cut <= 0 || cut >= len(conv) {
		t.Fatalf("cut = %d", cut)
	}
	if r := conv[cut].Role; r != "user" && r != "assistant" {
		t.Fatalf("cut lands on %q message", r)
	}
	if size := ApproxSize(conv[cut:]); size > 2600 {
		t.Fatalf("tail is %d bytes for keep=2000", size)
	}

	// a single enormous turn: no boundary fits keep, but the cut must
	// still make progress via the last boundary
	huge := []Message{
		{Role: "user", Content: "build it"},
		asstCall("bash", 100),
		toolMsg("bash", strings.Repeat("x", 50_000)),
		asstCall("bash", 100),
		toolMsg("bash", strings.Repeat("y", 50_000)),
	}
	cut = CompactCut(huge, 1000)
	if cut != 3 {
		t.Fatalf("huge-turn cut = %d, want 3 (the last assistant boundary)", cut)
	}
}

func TestApproxSizeCountsCodexItems(t *testing.T) {
	m := Message{Role: "assistant", Content: "hi", CodexItems: []json.RawMessage{json.RawMessage(strings.Repeat("r", 5000))}}
	if got := ApproxSize([]Message{m}); got < 5000 {
		t.Fatalf("ApproxSize = %d, encrypted reasoning not counted", got)
	}
}

func TestIsContextOverflow(t *testing.T) {
	for _, s := range []string{
		"ollama m: HTTP 400: this model's maximum context length is 131072 tokens",
		"chatgpt gpt: HTTP 400: prompt is too long",
		"context_length_exceeded",
	} {
		if !IsContextOverflow(errors.New(s)) {
			t.Errorf("not detected: %q", s)
		}
	}
	for _, s := range []string{"connection refused", "HTTP 429: rate limited"} {
		if IsContextOverflow(errors.New(s)) {
			t.Errorf("false positive: %q", s)
		}
	}
	if IsContextOverflow(nil) {
		t.Error("nil error detected as overflow")
	}
}

func TestRenderForDigestElides(t *testing.T) {
	seg := []Message{
		{Role: "user", Content: "build a guestbook"},
		asstCall("bash", 100),
		toolMsg("bash", strings.Repeat("z", 12_000)),
	}
	out := RenderForDigest("earlier summary", seg)
	if !strings.Contains(out, "earlier summary") || !strings.Contains(out, "build a guestbook") {
		t.Fatalf("render missing pieces: %.200s", out)
	}
	if len(out) > 5000 {
		t.Fatalf("render is %d bytes — tool output not elided", len(out))
	}
}
