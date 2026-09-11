package server

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// a rollout's first lines: the meta, Codex's preamble as user items, the
// prompt — the way the CLI writes them
func writeRollout(t *testing.T, home, day, id, originator, source, parent, cwd, prompt string, mtime time.Time) string {
	t.Helper()
	dir := filepath.Join(home, "sessions", "2026", "09", day)
	os.MkdirAll(dir, 0o755)
	path := filepath.Join(dir, "rollout-2026-09-"+day+"T10-00-00-"+id+".jsonl")
	meta := `{"timestamp":"2026-09-` + day + `T17:00:00.000Z","type":"session_meta","payload":{"id":"` + id + `","timestamp":"2026-09-` + day + `T17:00:00.000Z","cwd":"` + cwd + `","originator":"` + originator + `","cli_version":"0.153.4","source":` + source
	if parent != "" {
		meta += `,"parent_thread_id":"` + parent + `","thread_source":"guardian_review"`
	} else {
		meta += `,"thread_source":"user"`
	}
	meta += `}}` + "\n"
	body := meta +
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<recommended_plugins>\nHere is a list"}]}}` + "\n" +
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"# AGENTS.md instructions\n\n<INSTRUCTIONS>"}]}}` + "\n" +
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>\n  <cwd>` + cwd + `</cwd>"}]}}` + "\n" +
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"` + prompt + `"}]}}` + "\n" +
		`{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Sure."}]}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	os.Chtimes(path, mtime, mtime)
	return path
}

func TestCodexAppThreads(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 9, 10, 18, 0, 0, 0, time.UTC)
	phone := "01a088c7-26ed-7423-9f43-4012f591648b"
	named := writeRollout(t, home, "09", phone, "codex_chatgpt_ios_remote", `"vscode"`, "", "/www/exe",
		"i want you to create a new building set for /www/exe-city , but this time: 1. do not create every building in the set\\nmore", now.Add(-3*time.Second))
	// the CLI's own thread: not listed
	writeRollout(t, home, "09", "01a087c2-6a36-7761-bb3f-45a90b5ecde8", "codex-tui", `"cli"`, "", "/www/exe", "hello", now.Add(-time.Minute))
	// the phone thread's guardian review: a sub-agent, not listed
	writeRollout(t, home, "10", "01a08df0-9952-7483-a549-4d05fe89aeb4", "codex_chatgpt_ios_remote", `{"subagent":{"other":"guardian"}}`, phone, "/www/exe", "review", now.Add(-time.Second))
	// an older phone thread with no name in the index: titled by its prompt
	old := "01a0755c-a0f7-7313-8d1c-e6d5385fb06f"
	writeRollout(t, home, "05", old, "codex_chatgpt_ios_remote", `"vscode"`, "", "/home/livid/.codex/hub/claude-watch",
		"Livid requested one permanent Codex session dedicated to collaboration with Claude, so automated coordination works", now.Add(-4*24*time.Hour))
	// an exec run: not listed
	writeRollout(t, home, "10", "01a08c7c-6701-79a1-b399-2c0e85a1041d", "codex_exec", `"exec"`, "", "/tmp", "run", now)
	os.WriteFile(filepath.Join(home, "session_index.jsonl"), []byte(
		`{"id":"`+phone+`","thread_name":"i want you to create a new building","updated_at":"2026-09-10T00:49:20Z"}`+"\n"+
			`{"id":"`+phone+`","thread_name":"Create modern city building set","updated_at":"2026-09-10T00:49:22Z"}`+"\n"+
			`not json`+"\n"), 0o644)

	list := codexAppThreads(home, nil, now, codexThreadsShown)
	if len(list) != 2 {
		t.Fatalf("threads: %+v", list)
	}
	if l := list[0]; l.ID != phone || l.Title != "Create modern city building set" || l.Origin != "the ChatGPT app" ||
		l.Cwd != "/www/exe" || !l.Working || l.Activity != now.Add(-3*time.Second).Unix() || l.Created != time.Date(2026, 9, 9, 17, 0, 0, 0, time.UTC).Unix() {
		t.Fatalf("phone thread: %+v", l)
	}
	if l := list[1]; l.ID != old || l.Title != "Livid requested one permanent Codex session dedicated to col…" || l.Working {
		t.Fatalf("older thread: %+v", l)
	}
	// a session of the column's carries the phone thread: left out
	if list = codexAppThreads(home, map[string]bool{phone: true}, now, codexThreadsShown); len(list) != 1 || list[0].ID != old {
		t.Fatalf("with the phone thread live: %+v", list)
	}
	// the cap
	if list = codexAppThreads(home, nil, now, 1); len(list) != 1 || list[0].ID != phone {
		t.Fatalf("capped: %+v", list)
	}
	// the meta cache holds what was read; a rollout that changes id (it never does) would keep its first reading
	if _, ok := codexMetaCache.Load(named); !ok {
		t.Fatal("rollout not cached")
	}
	// no home, no sessions folder: nothing, quietly
	if list = codexAppThreads("", nil, now, 5); len(list) != 0 {
		t.Fatalf("no home: %+v", list)
	}
	if list = codexAppThreads(t.TempDir(), nil, now, 5); len(list) != 0 {
		t.Fatalf("empty home: %+v", list)
	}
}

func TestReadAgentThreadID(t *testing.T) {
	f := filepath.Join(t.TempDir(), "codex-2.status.json")
	if got := readAgentThreadID(f); got != "" {
		t.Fatalf("missing file: %q", got)
	}
	s := &Server{StateDir: t.TempDir()}
	a := hostAgents["codex"]
	s.noteCodexThread(a, "exe-codex-2", "01a088c7-26ed-7423-9f43-4012f591648b")
	if got := readAgentThreadID(s.agentStatusFile(a, "exe-codex-2")); got != "01a088c7-26ed-7423-9f43-4012f591648b" {
		t.Fatalf("stub: %q", got)
	}
	if !readAgentResumable(s.agentStatusFile(a, "exe-codex-2")) {
		t.Fatal("a resumed session is resumable from the start")
	}
	// the notify JSON of a finished turn reads the same way
	os.WriteFile(f, []byte(`{"type":"agent-turn-complete","thread-id":"abc-123","turn-id":"t1","last-assistant-message":"done"}`), 0o644)
	if got := readAgentThreadID(f); got != "abc-123" {
		t.Fatalf("notify JSON: %q", got)
	}
}
