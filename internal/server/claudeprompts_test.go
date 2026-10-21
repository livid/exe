package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// promptLine is a user entry of a transcript: content is its JSON, extra
// goes beside the message ("" for none).
func promptLine(ts, content, extra string) string {
	return `{"type":"user","timestamp":"` + ts + `","message":{"role":"user","content":` + content + `}` + extra + `,"uuid":"u"}`
}

func TestClaudePrompts(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	// the fixture: the CLI's own entries (a caveat flagged meta, a slash
	// command and its output), then the first prompt behind a reminder
	// block, a reply, and a tool's result
	path := writeClaudeTranscript(t, home, "-www-exe", "2b69001a-0639-473c-8765-a23c8df9da7f", "cli", "/www/exe", "add a tornado to the city", now)
	long := strings.Repeat("a paste of a long line, ", 12) // 288 runes: longer than Get Info's first line
	add := func(line string) { appendLine(t, path, line, now) }
	add(promptLine("2026-10-14T08:00:00Z", `"make it bigger\nmuch bigger"`, ""))
	add(promptLine("2026-10-14T08:01:00Z", `"`+long+`"`, ""))
	// several text blocks, the reminder in front left out, the rest one under another
	add(promptLine("2026-10-14T08:02:00Z", `[{"type":"text","text":"<system-reminder>\nnoted\n</system-reminder>"},{"type":"text","text":"first part"},{"type":"text","text":"second part"}]`, ""))
	// a prompt that opens with pasted markup is the person's
	add(promptLine("2026-10-14T08:03:00Z", `"<div class=\"x\">pasted</div> fix this"`, ""))
	// not prompts: a tool's result with text beside it, a sub-agent's, a
	// background task's notice, an interruption's note, a compaction's
	// summary (flagged, and an older one by its words), a shell command
	add(promptLine("2026-10-14T08:04:00Z", `[{"type":"tool_result","tool_use_id":"t9","content":"ok"},{"type":"text","text":"and this"}]`, ""))
	add(promptLine("2026-10-14T08:04:01Z", `"search the code"`, `,"isSidechain":true`))
	add(promptLine("2026-10-14T08:04:02Z", `"<task-notification>done</task-notification>"`, `,"origin":{"kind":"task-notification"}`))
	add(promptLine("2026-10-14T08:04:03Z", `"[Request interrupted by user]"`, ""))
	add(promptLine("2026-10-14T08:04:04Z", `"This session is being continued from a previous conversation that ran out of context."`, `,"isCompactSummary":true`))
	add(promptLine("2026-10-14T08:04:05Z", `"This session is being continued from a previous conversation that ran out of context."`, ""))
	add(promptLine("2026-10-14T08:04:06Z", `"<bash-input>ls</bash-input>"`, ""))
	// the person's, said so by its origin
	add(promptLine("2026-10-14T08:05:00Z", `"ship it"`, `,"origin":{"kind":"human"}`))

	ps := claudePromptsOf(path)
	want := []string{"add a tornado to the city", "make it bigger\nmuch bigger", strings.TrimSpace(long), "first part\nsecond part", `<div class="x">pasted</div> fix this`, "ship it"}
	if len(ps) != len(want) {
		t.Fatalf("%d prompts, want %d: %+v", len(ps), len(want), ps)
	}
	for i, w := range want {
		if ps[i].Text != w || ps[i].Cut {
			t.Errorf("prompt %d: %q cut=%v, want %q", i, ps[i].Text, ps[i].Cut, w)
		}
	}
	if ps[0].At != time.Date(2026, 9, 23, 6, 53, 13, 0, time.UTC).Unix() || ps[1].At != time.Date(2026, 10, 14, 8, 0, 0, 0, time.UTC).Unix() {
		t.Errorf("times %d %d", ps[0].At, ps[1].At)
	}
	// the cache: a second reading parses nothing and answers the same
	if again := claudePromptsOf(path); len(again) != len(ps) || &again[0] != &ps[0] {
		t.Error("a second reading, nothing appended: want the cached prompts")
	}
	// a line still being written waits; whole, it is the next prompt
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString(promptLine("2026-10-14T08:06:00Z", `"and then"`, ""))
	f.Close()
	if grown := claudePromptsOf(path); len(grown) != len(ps) {
		t.Errorf("a partial line counted: %d prompts", len(grown))
	}
	add("")
	grown := claudePromptsOf(path)
	if len(grown) != len(ps)+1 || grown[len(ps)].Text != "and then" {
		t.Fatalf("after growth: %d prompts, last %+v", len(grown), grown[len(grown)-1])
	}
	// a prompt past claudePromptMax is cut there and says so
	add(promptLine("2026-10-14T08:07:00Z", `"`+strings.Repeat("x", claudePromptMax+5)+`"`, ""))
	cut := claudePromptsOf(path)
	if last := cut[len(cut)-1]; !last.Cut || len([]rune(last.Text)) != claudePromptMax+1 || !strings.HasSuffix(last.Text, "…") {
		t.Errorf("cut: %v, %d runes", last.Cut, len([]rune(last.Text)))
	}
	// a shorter file under the same name is another: read from its start
	os.WriteFile(path, []byte(promptLine("2026-10-14T09:00:00Z", `"fresh start"`, "")+"\n"), 0o644)
	if fresh := claudePromptsOf(path); len(fresh) != 1 || fresh[0].Text != "fresh start" {
		t.Errorf("after truncation: %+v", fresh)
	}
	// a file that is not there
	if ps := claudePromptsOf(filepath.Join(home, "none.jsonl")); ps != nil {
		t.Errorf("no file: %+v", ps)
	}
}

// Get Info's turn text shares the rule: a wrapper of the CLI's is not a
// prompt, pasted markup is.
func TestClaudeTurnTextOwn(t *testing.T) {
	if got := claudeTurnText("<div>pasted</div>\nmore"); got != "<div>pasted</div>" {
		t.Errorf("markup: %q", got)
	}
	for _, s := range []string{"<command-name>/model</command-name>", "<local-command-stdout>x</local-command-stdout>", " <system-reminder>r</system-reminder>",
		"[Request interrupted by user]", "This session is being continued from a previous conversation that ran out of context.", "<ide_opened_file>f</ide_opened_file>"} {
		if got := claudeTurnText(s); got != "" {
			t.Errorf("%q: want none, got %q", s, got)
		}
	}
}

// the hub watcher's build prompts, in the shapes ~/.claude/hub/watch
// writes them: build.md for a reply on the hub, projbuild.md for a
// comment in Projects, and a later turn pasted behind its own line
const (
	watcherHubPrompt = "You are Claude's hub watcher, in build mode: Livid replied on the exe hub with feedback or an instruction about something you built. Nobody is at the keyboard.\n\n" +
		"New post(s):\n" +
		"- Post 03f354ac46b09f2d by Livid\n" +
		"  A reply in the thread of post e5aed1effceeb7aa (read that thread: http://hub.example/v1/post/e5aed1effceeb7aa)\n" +
		"  JSON: http://hub.example/v1/post/03f354ac46b09f2d   page: http://hub.example/p/03f354ac46b09f2d\n" +
		"  Picture: http://hub.example/v1/embed/bafy (image/png) — curl it to a file and Read it when it matters.\n" +
		"  Text:\n" +
		"    Let's continue. Here is what I want:\n" +
		"    \n" +
		"    - two sizes\n" +
		"  Post d4c535d5 under it is the watcher's own notice or \"On it\", not a session's reply: yours is still owed\n" +
		"- Post 77aa01 by Codex on Spark\n" +
		"  JSON: http://hub.example/v1/post/77aa01   page: http://hub.example/p/77aa01\n" +
		"  Text:\n" +
		"    I can take the palette part.\n\n" +
		"The thread starts at post e5aed1effceeb7aa. Read it first.\n\n" +
		"What to do:\n\n1. Read the feedback.\n"
	watcherProjPrompt = "You are Claude's watcher, in build mode. Livid commented on issue ROTK5-44 in Projects, and the comment is for you.\n\n" +
		"- Comment e4f8f6ff on issue ROTK5-44 by Livid\n" +
		"  The issue: \"68K music: silent after a track ends\", open, filed by Claude (read it whole: exe-projects show ROTK5-44)\n" +
		"  Text:\n" +
		"    Claude, handle this.\n" +
		"  Comment 5fa4626b after it is the watcher's own notice or \"On it\", not a session's comment: yours is still owed\n\n" +
		"The screener's reading, to check against the issue: a go-ahead.\n\n" +
		"    exe-projects show ROTK5-44\n"
)

func TestWatcherPromptInput(t *testing.T) {
	paste := "Hub watcher, Livid's automation: Livid replied in this thread on the hub. The pasted prompt below is your next build turn; do what it says. \n\n" +
		"<pasted_content id=\"5612\">\n" + watcherProjPrompt + "</pasted_content id=\"5612\">\n"
	for _, c := range []struct{ in, want string }{
		{watcherHubPrompt, "Let's continue. Here is what I want:\n\n- two sizes\n\nI can take the palette part."},
		{watcherProjPrompt, "Claude, handle this."},
		{paste, "Claude, handle this."},
	} {
		if got, ok := watcherPromptInput(c.in); !ok || got != c.want {
			t.Errorf("%.40q: got %q ok=%v, want %q", c.in, got, ok, c.want)
		}
	}
	// whole: a person's prompt, one that only quotes the orders, orders
	// with no item, and a pasted line of the watcher's with no prompt
	for _, s := range []string{
		"add a tornado to the city",
		"why does this say \"You are Claude's hub watcher\"?\n- Post 1 by Livid\n  Text:\n    hi",
		"You are Claude's hub watcher, in build mode.\n\nNew post(s):\n(none)\n",
		"You are Claude's helper.\n- Post 1 by Livid\n  Text:\n    hi",
		"Hub watcher, Livid's automation: do it.",
	} {
		if got, ok := watcherPromptInput(s); ok || got != s {
			t.Errorf("%.40q: want it whole, got %q ok=%v", s, got, ok)
		}
	}
	// the readers: Prompts and the line, Get Info, and a title
	if got := claudeTurnText(watcherProjPrompt); got != "Claude, handle this." {
		t.Errorf("Get Info: %q", got)
	}
	if got := claudePromptText(watcherHubPrompt); got != "Let's continue. Here is what I want:" {
		t.Errorf("title: %q", got)
	}
	home := t.TempDir()
	now := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	path := writeClaudeTranscript(t, home, "-www-exe", "5d6c0a1e-3f9b-4c2e-9a77-0e1f2b3c4d5e", "cli", "/www/exe", "a person's first words", now)
	in, _ := json.Marshal(watcherProjPrompt)
	pasted, _ := json.Marshal(paste)
	appendLine(t, path, promptLine("2026-10-21T08:00:00Z", string(in), `,"origin":{"kind":"human"}`), now)
	appendLine(t, path, promptLine("2026-10-21T08:30:00Z", string(pasted), ""), now)
	ps := claudePromptsOf(path)
	if len(ps) != 3 || ps[1].Text != "Claude, handle this." || ps[2].Text != "Claude, handle this." {
		t.Errorf("prompts: %+v", ps)
	}
}

func TestAgentLastPromptAndHandler(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	s := &Server{StateDir: t.TempDir()}
	a := hostAgents["claude"]
	os.MkdirAll(filepath.Join(s.StateDir, "agents"), 0o755)
	now := time.Now()
	path := writeClaudeTranscript(t, home, "-www-exe", "81afe071-7e89-4ee7-9522-b7e0ca11f6b6", "cli", "/www/exe", "add a tornado to the city", now)
	appendLine(t, path, promptLine("2026-10-14T08:00:00Z", `"make it bigger\nmuch bigger"`, ""), now)
	b, _ := json.Marshal(map[string]string{"session_id": "81afe071-7e89-4ee7-9522-b7e0ca11f6b6", "transcript_path": path})
	os.WriteFile(s.agentStatusFile(a, "exe-claude-2"), b, 0o644)

	// the frame's word: the latest prompt's first line, the count
	lp := s.agentLastPrompt(a, "exe-claude-2")
	if lp.Session != "exe-claude-2" || lp.N != 2 || lp.Text != "make it bigger" || lp.At != time.Date(2026, 10, 14, 8, 0, 0, 0, time.UTC).Unix() {
		t.Errorf("last prompt %+v", lp)
	}
	// a session with no conversation yet, and Codex, say so with n 0
	if lp := s.agentLastPrompt(a, "exe-claude-3"); lp.N != 0 || lp.Text != "" || lp.Session != "exe-claude-3" {
		t.Errorf("no conversation: %+v", lp)
	}
	if lp := s.agentLastPrompt(hostAgents["codex"], "exe-codex-2"); lp.N != 0 {
		t.Errorf("codex: %+v", lp)
	}

	get := func(app, name string) (int, string) {
		r := httptest.NewRequest(http.MethodGet, "/v1/agents/"+app+"/sessions/"+name+"/prompts", nil)
		r.SetPathValue("app", app)
		r.SetPathValue("name", name)
		w := httptest.NewRecorder()
		s.handleAgentSessionPrompts(w, r)
		return w.Code, w.Body.String()
	}
	code, body := get("claude", "exe-claude-2")
	var got struct {
		Prompts []claudePromptEntry `json:"prompts"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil || code != 200 {
		t.Fatalf("answer %d %q: %v", code, body, err)
	}
	if len(got.Prompts) != 2 || got.Prompts[0].Text != "add a tornado to the city" || got.Prompts[1].Text != "make it bigger\nmuch bigger" || got.Prompts[1].Cut {
		t.Errorf("prompts %+v", got.Prompts)
	}
	if code, body := get("claude", "exe-claude-3"); code != 404 || !strings.Contains(body, "no conversation yet") {
		t.Errorf("no conversation: %d %q", code, body)
	}
	if code, _ := get("claude", "something-else"); code != 400 {
		t.Errorf("not a session: %d", code)
	}
	if code, body := get("codex", "exe-codex-2"); code != 404 || !strings.Contains(body, "Codex") {
		t.Errorf("codex: %d %q", code, body)
	}
}
