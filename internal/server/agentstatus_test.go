package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// the follower over a real WebSocket: the figures on file arrive on
// connect, each rewrite arrives once, and other JSON in the file does not
func TestPushAgentStatus(t *testing.T) {
	file := filepath.Join(t.TempDir(), "claude.status.json")
	hook := func(model string) string {
		return `{"model":{"display_name":"` + model + `"},"context_window":{"context_window_size":1000000,"used_percentage":null},"cost":{}}`
	}
	os.WriteFile(file, []byte(hook("First")), 0o644)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		pushAgentStatus(ctx, &wsWriter{ctx: ctx, c: c}, file)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	frame := func(want string) {
		t.Helper()
		typ, data, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("waiting for %s: %v", want, err)
		}
		if typ != websocket.MessageText || !strings.Contains(string(data), `"model":"`+want+`"`) {
			t.Fatalf("got %v %s, want a text frame for %s", typ, data, want)
		}
	}
	frame("First")
	time.Sleep(1100 * time.Millisecond) // past the poll, so the rewrite is a second change
	os.WriteFile(file+".tmp", []byte(hook("Second")), 0o644)
	os.Rename(file+".tmp", file)
	frame("Second")

	// junk in the file sends nothing; the next real version does
	time.Sleep(1100 * time.Millisecond)
	os.WriteFile(file, []byte(`{"resize":[80,24]}`), 0o644)
	time.Sleep(1100 * time.Millisecond)
	os.WriteFile(file, []byte(hook("Third")), 0o644)
	frame("Third")
}

// the hook settings an agent is launched with: the bridge alone, the
// user's own status line chained in, none for an agent without a hook
func TestAgentStatusArgs(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "claude.status.json")
	tmp := shQuote(file + ".tmp")
	parse := func(args []string) (cmd string, padding *int) {
		t.Helper()
		if len(args) != 2 || args[0] != "--settings" {
			t.Fatalf("args = %q, want --settings <json>", args)
		}
		var st struct {
			StatusLine struct {
				Type    string `json:"type"`
				Command string `json:"command"`
				Padding *int   `json:"padding"`
			} `json:"statusLine"`
		}
		if err := json.Unmarshal([]byte(args[1]), &st); err != nil {
			t.Fatalf("settings %q: %v", args[1], err)
		}
		if st.StatusLine.Type != "command" {
			t.Fatalf("type = %q, want command", st.StatusLine.Type)
		}
		return st.StatusLine.Command, st.StatusLine.Padding
	}

	cmd, padding := parse(agentStatusArgs(hostAgents["claude"], file, filepath.Join(dir, "none.json")))
	if want := "cat > " + tmp + "; mv -f " + tmp + " " + shQuote(file); cmd != want || padding != nil {
		t.Fatalf("no user status line: command = %q padding = %v, want %q and none", cmd, padding, want)
	}

	settings := filepath.Join(dir, "settings.json")
	os.WriteFile(settings, []byte(`{"statusLine":{"type":"command","command":"echo it's mine","padding":0}}`), 0o644)
	cmd, padding = parse(agentStatusArgs(hostAgents["claude"], file, settings))
	want := "cat > " + tmp + "; sh -c " + shQuote("echo it's mine") + " < " + tmp + "; mv -f " + tmp + " " + shQuote(file)
	if cmd != want {
		t.Fatalf("user status line: command = %q, want %q", cmd, want)
	}
	if padding == nil || *padding != 0 {
		t.Fatalf("padding = %v, want the user's 0", padding)
	}

	if args := agentStatusArgs(hostAgents["codex"], file, settings); args != nil {
		t.Fatalf("codex has no hook, got %q", args)
	}
}

// the bridge as sh runs it: the JSON lands whole in the file, the user's
// own status line still draws from it, and the temp file is gone
func TestAgentStatusBridge(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the bridge is a sh one-liner")
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "it's here", "claude.status.json") // the quote proves the quoting
	os.MkdirAll(filepath.Dir(file), 0o755)
	settings := filepath.Join(dir, "settings.json")
	os.WriteFile(settings, []byte(`{"statusLine":{"type":"command","command":"printf 'mine: '; head -c 9"}}`), 0o644)
	args := agentStatusArgs(hostAgents["claude"], file, settings)
	var st struct {
		StatusLine struct {
			Command string `json:"command"`
		} `json:"statusLine"`
	}
	json.Unmarshal([]byte(args[1]), &st)

	cmd := exec.Command("sh", "-c", st.StatusLine.Command)
	cmd.Stdin = strings.NewReader(`{"model":{}}`)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("bridge: %v", err)
	}
	if string(out) != `mine: {"model":` {
		t.Fatalf("user status line drew %q", out)
	}
	if b, err := os.ReadFile(file); err != nil || string(b) != `{"model":{}}` {
		t.Fatalf("status file = %q, %v", b, err)
	}
	if _, err := os.Stat(file + ".tmp"); err == nil {
		t.Fatal("temp file left behind")
	}
}

// the frame the window gets: the figures it shows, the plan windows it
// knows, null context before the first reply, nothing for other JSON
func TestAgentStatusMessage(t *testing.T) {
	reply := `{"session_id":"x","model":{"id":"claude-fable-5-1","display_name":"Fable 5.1"},
	  "context_window":{"total_input_tokens":41230,"total_output_tokens":3010,"context_window_size":1000000,
	    "current_usage":{"input_tokens":2,"cache_read_input_tokens":300000},"used_percentage":34.5,"remaining_percentage":65.5},
	  "cost":{"total_cost_usd":1.2,"total_duration_ms":5},
	  "rate_limits":{"five_hour":{"used_percentage":12,"resets_at":1756800000},"seven_day":{"used_percentage":3,"resets_at":1757000000},
	    "spend_limit":{"used_percentage":0}}}`
	msg, ok := agentStatusMessage([]byte(reply))
	if !ok {
		t.Fatal("a reply's JSON was not taken")
	}
	var got struct {
		Status agentStatus `json:"status"`
	}
	if err := json.Unmarshal(msg, &got); err != nil {
		t.Fatalf("frame %s: %v", msg, err)
	}
	st := got.Status
	if st.Model != "Fable 5.1" || st.ContextPct == nil || *st.ContextPct != 34.5 || st.ContextSize != 1000000 ||
		st.InputTokens != 41230 || st.OutputTokens != 3010 || st.CostUSD != 1.2 {
		t.Fatalf("figures: %+v", st)
	}
	if len(st.Limits) != 2 || st.Limits["five_hour"].UsedPct != 12 || string(st.Limits["five_hour"].ResetsAt) != "1756800000" ||
		st.Limits["seven_day"].UsedPct != 3 {
		t.Fatalf("limits: %+v", st.Limits)
	}

	launch := `{"model":{"display_name":"Fable 5.1"},"context_window":{"total_input_tokens":0,"total_output_tokens":0,
	  "context_window_size":1000000,"current_usage":null,"used_percentage":null,"remaining_percentage":null},
	  "cost":{"total_cost_usd":0},"rate_limits":null}`
	msg, ok = agentStatusMessage([]byte(launch))
	if !ok {
		t.Fatal("the launch JSON was not taken")
	}
	if !strings.Contains(string(msg), `"context_pct":null`) || strings.Contains(string(msg), `"limits"`) {
		t.Fatalf("launch frame: %s", msg)
	}

	for _, bad := range []string{`{"resize":[80,24]}`, `garbage`, ``} {
		if _, ok := agentStatusMessage([]byte(bad)); ok {
			t.Fatalf("%q was taken for the hook's JSON", bad)
		}
	}
}
