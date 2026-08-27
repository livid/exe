package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"exe/internal/sshexec"
)

// A tool call with "arguments": null must yield a writable map: JSON null
// unmarshals into a nil map with no error, and callers (pinChatArgs) write
// into the result — a nil map there panicked the daemon.
func TestParseArgsNull(t *testing.T) {
	for _, raw := range []string{"null", `"null"`, "", "{}", `{"vm":"a"}`} {
		args := ParseArgs([]byte(raw))
		if args == nil {
			t.Fatalf("ParseArgs(%q) returned nil", raw)
		}
		args["vm"] = "x" // must not panic
	}
}

// Transient failures (5xx, 429) are retried with backoff inside chatHTTP;
// a request that succeeds within maxAttempts must not surface an error.
func TestChatRetriesTransient(t *testing.T) {
	old := retryBase
	retryBase = time.Millisecond
	defer func() { retryBase = old }()

	fails := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fails < 2 {
			fails++
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte(`{"message":{"role":"assistant","content":"hi"}}`))
	}))
	defer srv.Close()

	resp, err := Chat(context.Background(), Config{BaseURL: srv.URL, Model: "m"},
		[]Message{{Role: "user", Content: "x"}}, nil)
	if err != nil {
		t.Fatalf("Chat after transient failures: %v", err)
	}
	if fails != 2 || resp.Message.Content != "hi" {
		t.Fatalf("fails=%d content=%q", fails, resp.Message.Content)
	}
}

// A server that never recovers still errors out after maxAttempts.
func TestChatRetriesExhausted(t *testing.T) {
	old := retryBase
	retryBase = time.Millisecond
	defer func() { retryBase = old }()

	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := Chat(context.Background(), Config{BaseURL: srv.URL, Model: "m"}, nil, nil)
	if err == nil || calls != maxAttempts {
		t.Fatalf("err=%v calls=%d, want error after %d attempts", err, calls, maxAttempts)
	}
}

func TestRequireArgs(t *testing.T) {
	if msg := RequireArgs(map[string]any{"path": "/a"}, "path"); msg != "" {
		t.Fatalf("unexpected: %q", msg)
	}
	msg := RequireArgs(map[string]any{"command": "  "}, "command")
	if !strings.Contains(msg, "command") {
		t.Fatalf("missing arg not reported: %q", msg)
	}
	if msg := RequireArgs(map[string]any{}); msg != "" {
		t.Fatalf("no keys must pass: %q", msg)
	}
}

// A malformed tool call errors back to the model before touching SSH — an
// empty bash command must never run.
func TestExecToolMalformed(t *testing.T) {
	logf := func(string, ...any) {}
	tc := ToolCall{}
	tc.Function.Name = "bash"
	tc.Function.Arguments = []byte(`{}`)
	out := execTool(context.Background(), sshexec.Target{}, tc, logf)
	if !strings.Contains(out, "missing required argument") || !strings.Contains(out, "command") {
		t.Fatalf("bash{} = %q", out)
	}
	tc.Function.Name = "write_file"
	tc.Function.Arguments = []byte(`{"path":"/tmp/x"}`)
	if out := execTool(context.Background(), sshexec.Target{}, tc, logf); !strings.Contains(out, "content") {
		t.Fatalf("write_file without content = %q", out)
	}
}
