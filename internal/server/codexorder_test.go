package server

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestCodexInteraction(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	check := func(want int64) {
		t.Helper()
		if got := agentTrailOf(path, codexTrailLine).interaction; got != want {
			t.Fatalf("last interaction = %d, want %d", got, want)
		}
	}
	stamp := "2026-10-29T10:00:00.123Z"
	writeLines(t, path, codexUser(stamp, "# AGENTS.md instructions for /example"),
		codexUser(stamp, "<environment_context>example</environment_context>"),
		codexEvent(stamp, "task_started"))
	check(0)
	writeLines(t, path, codexUser(stamp, "please fix this"))
	check(1793268000123)
	// Output, context injected on resume, compaction, and notifications
	// cannot make an old conversation look newly messaged.
	later := "2026-10-29T10:01:00.456Z"
	writeLines(t, path,
		strings.Replace(codexUser(later, "reply"), `"role":"user"`, `"role":"assistant"`, 1),
		`{"timestamp":"`+later+`","type":"response_item","payload":{"type":"function_call_output","output":"done"}}`,
		`{"timestamp":"`+later+`","type":"compacted","payload":{"message":"summary"}}`,
		codexUser(later, "<environment_context>updated</environment_context>"),
		codexUser(later, "<subagent_notification>finished</subagent_notification>"),
		codexEvent(later, "task_complete"))
	check(1793268000123)
	// A heading is a valid prompt, including one sent during a turn.
	writeLines(t, path, codexEvent(later, "task_started"), codexUser(later, "# Please check the sidebar"))
	check(1793268060456)
	later = "2026-10-29T10:01:00.789Z"
	writeLines(t, path, codexUser(later, "<example>check this XML</example>"))
	check(1793268060789)
	writeLines(t, path, codexUser("bad timestamp", "continue"))
	check(1793268060789)
	// An image-only message counts too.
	image := `{"timestamp":"2026-10-29T10:02:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,AA=="}]}}`
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(image[:50])
	check(1793268060789) // wait for a complete log line
	f.WriteString(image[50:] + "\n")
	f.Close()
	check(1793268120000)
	// Truncating a rollout clears the previous conversation's timestamp.
	if err := os.WriteFile(path, []byte(codexUser(stamp, "fresh")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	check(1793268000123)
}

func TestCodexColumnInteractionOrder(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("tmux fixture uses a POSIX shell")
	}
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	// No command from this test can reach the user's tmux server.
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	s := &Server{StateDir: t.TempDir()}
	a := hostAgents["codex"]
	var panes []string
	paths := map[int]string{}
	for n := 1; n <= 7; n++ {
		name := agentSessionName(a, n)
		created := n * 100
		if n == 4 {
			created = 300 // same creation time and message as 3: number wins
		}
		panes = append(panes, fmt.Sprintf("%s:%d:9999999999:1:0:9999999999:Example %d", name, created, n))
		id := fmt.Sprintf("d4290000-0000-4000-8000-%012d", n)
		t.Cleanup(func() { codexRolloutPaths.Delete(id) })
		path := filepath.Join(home, "sessions", "2026", "10", "29", "rollout-example-"+id+".jsonl")
		paths[n] = path
		writeLines(t, path, codexUser("2026-10-29T09:00:00Z", "<environment_context>context</environment_context>"))
		s.noteCodexThread(a, name, id)
		if n <= 5 {
			stamp := "2026-10-29T10:00:00.100Z"
			if n == 1 {
				stamp = "2026-10-29T10:00:00.200Z" // subsecond order matters
			}
			writeLines(t, path, codexUser(stamp, "check this"))
		}
	}
	script := "#!/bin/sh\nif [ \"$1\" = list-panes ]; then\ncat <<'ROWS'\n" + strings.Join(panes, "\n") + "\nROWS\nelse\nexit 1\nfi\n"
	// Use an absolute cat path since PATH is deliberately isolated.
	script = strings.Replace(script, "cat <<", "/bin/cat <<", 1)
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	read := func(want []int) {
		t.Helper()
		r := httptest.NewRequest("GET", "/v1/agents/codex/sessions", nil)
		r.SetPathValue("app", "codex")
		w := httptest.NewRecorder()
		s.handleAgentSessionsList(w, r)
		var body struct {
			Sessions []agentSession `json:"sessions"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code != 200 {
			t.Fatalf("list: status %d, error %v", w.Code, err)
		}
		var got []int
		for _, row := range body.Sessions {
			got = append(got, row.Number)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("column order = %v, want %v", got, want)
		}
	}
	read([]int{1, 5, 4, 3, 2, 7, 6})
	// New assistant output never promotes session 2; a new user message does.
	writeLines(t, paths[2], strings.Replace(codexUser("2026-10-29T11:00:00Z", "reply"), `"role":"user"`, `"role":"assistant"`, 1))
	read([]int{1, 5, 4, 3, 2, 7, 6})
	writeLines(t, paths[2], codexUser("2026-10-29T11:00:01Z", "continue"))
	read([]int{2, 1, 5, 4, 3, 7, 6})
	// A stopped session keeps the same message order; Claude's stays by start.
	rows := []agentSession{{Name: "exe-codex", Number: 1, Created: 100, Stopped: true}, {Name: "exe-codex-2", Number: 2, Created: 200}}
	s.orderSessionColumn(a, rows)
	if rows[0].Number != 2 || !rows[1].Stopped {
		t.Fatalf("stopped row: %+v", rows)
	}
	claude := []agentSession{{Number: 2, Created: 200}, {Number: 1, Created: 100}}
	s.orderSessionColumn(hostAgents["claude"], claude)
	if claude[0].Number != 2 {
		t.Fatalf("Claude order changed: %+v", claude)
	}
}
