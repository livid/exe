package server

import (
	"encoding/json"
	"strings"
	"testing"

	"exe/internal/agent"
	"exe/internal/chat"
)

func TestChatDigest(t *testing.T) {
	tc := agent.ToolCall{}
	tc.Function.Name = "bash"
	tc.Function.Arguments = json.RawMessage(`{"vm":"test","command":"uname -a"}`)
	sess := &chat.Session{Messages: []agent.Message{
		{Role: "user", Content: "what   kernel\nis this"},
		{Role: "assistant", ToolCalls: []agent.ToolCall{tc}},
		{Role: "tool", Content: "Linux test 6.1.0 aarch64\n"},
		{Role: "assistant", Content: "It runs Linux 6.1.0."},
	}}
	d := chatDigest(sess)
	want := "User: what kernel is this\nRan: test $ uname -a\nResult: Linux test 6.1.0 aarch64\nAgent: It runs Linux 6.1.0."
	if d != want {
		t.Fatalf("digest =\n%s\nwant\n%s", d, want)
	}

	// a huge session keeps the opening ask and the most recent work
	for i := 0; i < 200; i++ {
		sess.Messages = append(sess.Messages, agent.Message{Role: "tool", Content: strings.Repeat("x", 400)})
	}
	sess.Messages = append(sess.Messages, agent.Message{Role: "assistant", Content: "done at the end"})
	d = chatDigest(sess)
	if len(d) > chatDigestHead+chatDigestTail+10 {
		t.Fatalf("digest not capped: %d bytes", len(d))
	}
	if !strings.HasPrefix(d, "User: what kernel is this") || !strings.HasSuffix(d, "done at the end") {
		t.Fatalf("digest lost head or tail:\n%.80s\n…\n%s", d, d[len(d)-80:])
	}
}

func TestTrimSummary(t *testing.T) {
	if got := trimSummary("  \"Built a  snake game\non port 8000.\" "); got != "Built a snake game on port 8000." {
		t.Fatalf("got %q", got)
	}
	long := trimSummary(strings.Repeat("word ", 100))
	if len(long) > chatSummaryChars+4 || !strings.HasSuffix(long, "…") {
		t.Fatalf("not capped: %q (%d)", long, len(long))
	}
}
