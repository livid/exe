package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"exe/internal/codex"
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
		pushAgentStatus(ctx, &wsWriter{ctx: ctx, c: c}, func() string { return file })
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

// the usage feed over a real WebSocket: the figures arrive on connect and
// each tick after; a failed read clears them once, then stays quiet until
// a read succeeds again; failing from the start sends nothing
func TestPushOpenAIUsage(t *testing.T) {
	var mu sync.Mutex
	var answers []*codex.Usage // nil = the read fails
	fetch := func(context.Context) (*codex.Usage, error) {
		mu.Lock()
		defer mu.Unlock()
		if len(answers) == 0 {
			return nil, errors.New("not signed in")
		}
		u := answers[0]
		answers = answers[1:]
		if u == nil {
			return nil, errors.New("HTTP 502")
		}
		return u, nil
	}
	usage := func(pct float64) *codex.Usage {
		return &codex.Usage{PlanType: "plus", RateLimit: &codex.UsageRateLimit{
			PrimaryWindow: &codex.UsageWindow{UsedPercent: pct, LimitWindowSeconds: 18000},
		}}
	}
	mu.Lock()
	answers = []*codex.Usage{nil, usage(12), usage(13), nil, nil, usage(14)}
	mu.Unlock()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		pushOpenAIUsage(ctx, &wsWriter{ctx: ctx, c: c}, fetch, 100*time.Millisecond)
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
		if typ != websocket.MessageText || !strings.Contains(string(data), want) {
			t.Fatalf("got %v %s, want a text frame with %s", typ, data, want)
		}
	}
	// the first read fails with nothing shown: no frame; then two reads
	frame(`"used_percent":12`)
	frame(`"used_percent":13`)
	// two failures clear the figures once
	frame(`{"usage":null}`)
	frame(`"used_percent":14`)
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

	// Codex gets its config overrides instead (TestCodexArgs)
	args := agentStatusArgs(hostAgents["codex"], filepath.Join(dir, "codex.status.json"), filepath.Join(dir, "none.toml"))
	if len(args) != 10 || args[0] != "-c" || !strings.HasPrefix(args[1], "notify=[") {
		t.Fatalf("codex args = %q", args)
	}
}

// Codex's overrides, and the notify bridge as sh runs it with the JSON
// Codex passes: the JSON lands whole in the status file, which makes the
// session resumable, the state file says done, no temp file is left,
// and a notify of the user's own runs after with the same argument
func TestCodexArgs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the bridge is a sh one-liner")
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "it's here", "codex-2.status.json") // the quote proves the quoting
	os.MkdirAll(filepath.Dir(file), 0o755)
	config := filepath.Join(dir, "config.toml")
	mine := filepath.Join(dir, "mine.json")
	os.WriteFile(config, []byte("model = \"gpt-6-astra\"\nnotify = [\"sh\", \"-c\", \"printf %s \\\"$1\\\" > '"+mine+"'\", \"mine\"]\n\n[tui]\nnotifications = false\n"), 0o644)
	args := codexArgs(file, config)
	want := map[string]bool{"tui.notifications=true": false, `tui.notification_method="bel"`: false,
		`tui.notification_condition="always"`: false, `tui.terminal_title=["activity","thread-title"]`: false}
	var notify []string
	for i := 0; i+1 < len(args); i += 2 {
		if args[i] != "-c" {
			t.Fatalf("args = %q: every override is a -c pair", args)
		}
		if v, ok := strings.CutPrefix(args[i+1], "notify="); ok {
			list, ok := tomlStringArray(v)
			if !ok {
				t.Fatalf("notify is not a TOML array of strings: %s", v)
			}
			notify = list
			continue
		}
		if _, ok := want[args[i+1]]; !ok {
			t.Fatalf("unexpected override %q", args[i+1])
		}
		want[args[i+1]] = true
	}
	for k, seen := range want {
		if !seen {
			t.Errorf("missing override %s", k)
		}
	}
	if len(notify) != 4 || notify[0] != "sh" || notify[1] != "-c" || notify[3] != "exe-notify" {
		t.Fatalf("notify = %q", notify)
	}
	payload := `{"type":"agent-turn-complete","thread-id":"01a0783c-153f-7ab3-b9da-5d7302aed8c7","input-messages":["it's \"quoted\""],"last-assistant-message":"ok"}`
	cmd := exec.Command(notify[0], append(notify[1:], payload)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bridge: %v: %s", err, out)
	}
	if b, err := os.ReadFile(file); err != nil || string(b) != payload {
		t.Fatalf("status file = %q, %v", b, err)
	}
	if !readAgentResumable(file) {
		t.Error("a thread that has finished a turn should be resumable")
	}
	if got := readAgentState(stateFileOf(file)); got != agentStateDone {
		t.Errorf("state = %q, want done", got)
	}
	for _, p := range []string{file + ".tmp", stateFileOf(file) + ".tmp"} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("temp file left behind: %s", p)
		}
	}
	if b, err := os.ReadFile(mine); err != nil || string(b) != payload {
		t.Fatalf("the user's own notify got %q, %v", b, err)
	}

	// no config, no user notify: the bridge is the whole command
	args = codexArgs(file, filepath.Join(dir, "none.toml"))
	list, _ := tomlStringArray(strings.TrimPrefix(args[1], "notify="))
	if len(list) != 4 || strings.Contains(list[2], "exec") {
		t.Errorf("without a user notify the script should not exec one: %q", list)
	}
}

// the user's notify out of config.toml: one line, many lines with
// comments, literal strings, escapes; nothing for none, for one inside
// a table, or for one this reader cannot follow
func TestCodexNotify(t *testing.T) {
	dir := t.TempDir()
	read := func(body string) []string {
		p := filepath.Join(dir, "config.toml")
		os.WriteFile(p, []byte(body), 0o644)
		return codexNotify(p)
	}
	eq := func(got []string, want ...string) bool {
		if len(got) != len(want) {
			return false
		}
		for i := range got {
			if got[i] != want[i] {
				return false
			}
		}
		return true
	}
	if got := codexNotify(filepath.Join(dir, "missing.toml")); got != nil {
		t.Errorf("missing config: %q", got)
	}
	if got := read("model = \"x\"\n"); got != nil {
		t.Errorf("no notify: %q", got)
	}
	if got := read("notify = [\"python3\", \"/home/me/notify.py\"] # mine\n"); !eq(got, "python3", "/home/me/notify.py") {
		t.Errorf("one line: %q", got)
	}
	if got := read("notify = [\n  'sh', # the shell\n  \"-c\",\n  \"say \\\"done\\\" \\u2713\",\n]\n[tui]\n"); !eq(got, "sh", "-c", `say "done" ✓`) {
		t.Errorf("many lines: %q", got)
	}
	if got := read("[tui]\nnotify = [\"x\"]\n"); got != nil {
		t.Errorf("inside a table: %q", got)
	}
	if got := read("notify = [\"x\"\n"); got != nil {
		t.Errorf("unclosed: %q", got)
	}
	if got := read("notify = \"x\"\n"); got != nil {
		t.Errorf("not an array: %q", got)
	}
	if got := read("notify = []\n"); got == nil || len(got) != 0 {
		t.Errorf("empty array: %#v", got)
	}
	// the writer round-trips through the reader, whatever is in the strings
	odd := []string{"sh", "-c", "printf %s \"$1\" > 'a\\b' \t\n", "tab\there", "ünïcode ✓"}
	if got, ok := tomlStringArray(tomlStrings(odd)); !ok || !eq(got, odd...) {
		t.Errorf("round trip: %q -> %q (%v)", odd, got, ok)
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

// The settings JSON carries the session's state hooks beside the status
// line, each writing its word into the state file and moving it into
// place; a Notification matcher names the kinds that ask for the person.
func TestAgentStateHooks(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "claude-2.status.json")
	args := agentStatusArgs(hostAgents["claude"], file, filepath.Join(dir, "none.json"))
	var st struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type, Command string
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(args[1]), &st); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(dir, "claude-2.state")
	for event, word := range map[string]string{"UserPromptSubmit": "working", "Stop": "done", "StopFailure": "done", "Notification": "waiting"} {
		h := st.Hooks[event]
		if len(h) != 1 || len(h[0].Hooks) != 1 || h[0].Hooks[0].Type != "command" {
			t.Fatalf("%s hooks = %+v", event, h)
		}
		// run the hook the way Claude Code would: the file holds the word after
		if out, err := exec.Command("sh", "-c", h[0].Hooks[0].Command).CombinedOutput(); err != nil {
			t.Fatalf("%s hook: %v: %s", event, err, out)
		}
		if got := readAgentState(state); got != word {
			t.Errorf("%s wrote %q, want %q", event, got, word)
		}
	}
	if m := st.Hooks["Notification"][0].Matcher; !strings.Contains(m, "permission_prompt") || !strings.Contains(m, "idle_prompt") {
		t.Errorf("Notification matcher = %q", m)
	}
	if st.Hooks["Stop"][0].Matcher != "" {
		t.Errorf("Stop should match every stop, got matcher %q", st.Hooks["Stop"][0].Matcher)
	}
	os.WriteFile(state, []byte("nonsense"), 0o644)
	if got := readAgentState(state); got != "" {
		t.Errorf("a stray word reads as %q, want none", got)
	}
}

func TestMarkAgentStates(t *testing.T) {
	s := &Server{StateDir: t.TempDir()}
	a := hostAgents["claude"]
	os.MkdirAll(filepath.Join(s.StateDir, "agents"), 0o755)
	os.WriteFile(s.agentStateFile(a, "exe-claude"), []byte("done"), 0o644)
	os.WriteFile(s.agentStateFile(a, "exe-claude-2"), []byte("working\n"), 0o644)
	os.WriteFile(s.agentStateFile(a, "exe-claude-3"), []byte("waiting"), 0o644)
	os.WriteFile(s.agentStateFile(a, "exe-claude-5"), []byte("done"), 0o644)
	list := []agentSession{
		{Name: "exe-claude", Working: true},    // repainting, but its turn is over
		{Name: "exe-claude-2", Working: false}, // silent inside a long tool run
		{Name: "exe-claude-3", Working: true},
		{Name: "exe-claude-4", Working: true},                // no file: the activity guess stands
		{Name: "exe-claude-5", Working: true, spinner: true}, // the title's spinner: the next turn is on, the word is old
	}
	s.markAgentStates(a, list)
	want := []agentSession{
		{Name: "exe-claude", State: "done", Working: false, Wants: true},
		{Name: "exe-claude-2", State: "working", Working: true},
		{Name: "exe-claude-3", State: "waiting", Working: false, Wants: true},
		{Name: "exe-claude-4", Working: true},
		{Name: "exe-claude-5", Working: true, spinner: true},
	}
	for i := range want {
		if list[i] != want[i] {
			t.Errorf("%s: got %+v, want %+v", want[i].Name, list[i], want[i])
		}
	}
	if got := s.agentStateFile(a, "exe-claude-2"); filepath.Base(got) != "claude-2.state" {
		t.Errorf("state file = %q", got)
	}
}

// Archive is offered only for a conversation the CLI's resume picker can
// find: the status file names the session and a transcript that exists.
func TestReadAgentResumable(t *testing.T) {
	dir := t.TempDir()
	transcript := filepath.Join(dir, "abc.jsonl")
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte(body), 0o644)
		return p
	}
	if readAgentResumable(filepath.Join(dir, "missing.json")) {
		t.Error("no status file should not be resumable")
	}
	fresh := write("fresh.json", `{"session_id":"abc","transcript_path":"`+transcript+`","context_window":{"total_input_tokens":0}}`)
	if readAgentResumable(fresh) {
		t.Error("a session whose transcript is not written yet should not be resumable")
	}
	os.WriteFile(transcript, []byte("{}\n"), 0o644)
	if !readAgentResumable(fresh) {
		t.Error("a session with a transcript on disk should be resumable")
	}
	if readAgentResumable(write("noid.json", `{"transcript_path":"`+transcript+`"}`)) {
		t.Error("no session id should not be resumable")
	}
	if !readAgentResumable(write("nopath.json", `{"session_id":"abc","context_window":{"total_input_tokens":12}}`)) {
		t.Error("tokens exchanged without a transcript path should count")
	}
	if !readAgentResumable(write("codex.json", `{"type":"agent-turn-complete","thread-id":"01a0783c-153f-7ab3-b9da-5d7302aed8c7","turn-id":"x"}`)) {
		t.Error("a Codex thread named by its notify should be resumable")
	}
	if readAgentResumable(write("codex-empty.json", `{"type":"agent-turn-complete","thread-id":""}`)) {
		t.Error("an empty thread id should not be resumable")
	}
	s := &Server{StateDir: dir}
	a := hostAgents["claude"]
	os.MkdirAll(filepath.Join(dir, "agents"), 0o755)
	os.WriteFile(s.agentStatusFile(a, "exe-claude-2"), []byte(`{"session_id":"abc","transcript_path":"`+transcript+`"}`), 0o644)
	list := []agentSession{{Name: "exe-claude"}, {Name: "exe-claude-2"}}
	s.markAgentStates(a, list)
	if list[0].Resumable || !list[1].Resumable {
		t.Errorf("resumable = %v %v, want false true", list[0].Resumable, list[1].Resumable)
	}
}
