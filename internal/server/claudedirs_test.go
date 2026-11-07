package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The New Session dialog's folders: the latest a person worked in first,
// each once, headless runs and folders gone from the machine left out.
func TestClaudeDirs(t *testing.T) {
	home, work := t.TempDir(), t.TempDir()
	now := time.Now()
	mk := func(name string) string {
		d := filepath.Join(work, name)
		os.MkdirAll(d, 0o755)
		return d
	}
	exe, city, tiger, paint := mk("exe"), mk("exe-city"), mk("tiger"), mk("studios-1")
	id := func(n int) string { return "81afe071-7e89-4ee7-9522-" + strings.Repeat("0", 11) + string(rune('0'+n)) }
	writeClaudeTranscript(t, home, "-exe", id(1), "cli", exe, "fix the status bar", now.Add(-2*time.Hour))
	writeClaudeTranscript(t, home, "-exe", id(2), "cli", exe, "add a dialog", now.Add(-time.Hour))
	// the latest in exe-city is a headless run: the folder's time is its
	// latest interactive session's
	writeClaudeTranscript(t, home, "-exe-city", id(3), "sdk-cli", city, "You are Claude, running the nightly idea routine", now)
	writeClaudeTranscript(t, home, "-exe-city", id(4), "cli", city, "add a tornado", now.Add(-3*time.Hour))
	// a painter's studio: headless runs alone
	writeClaudeTranscript(t, home, "-studios-1", id(5), "sdk-cli", paint, "paint", now.Add(-time.Minute))
	// a folder since removed
	writeClaudeTranscript(t, home, "-gone", id(6), "cli", filepath.Join(work, "gone"), "hello", now.Add(-30*time.Minute))
	writeClaudeTranscript(t, home, "-tiger", id(7), "cli", tiger, "build gcc", now.Add(-4*time.Hour))

	got := claudeDirs(home, 10)
	want := []string{exe, city, tiger}
	if len(got) != len(want) {
		t.Fatalf("dirs: %+v, want %v", got, want)
	}
	for i, d := range got {
		if d.Path != want[i] {
			t.Fatalf("dir %d: %q, want %q (%+v)", i, d.Path, want[i], got)
		}
	}
	if got[0].At != now.Add(-time.Hour).Unix() || got[1].At != now.Add(-3*time.Hour).Unix() {
		t.Fatalf("times: %+v", got)
	}
	if got := claudeDirs(home, 1); len(got) != 1 || got[0].Path != exe {
		t.Fatalf("limit 1: %+v", got)
	}
	if got := claudeDirs("", 10); len(got) != 0 {
		t.Fatalf("no home: %+v", got)
	}
	// a transcript with no user entry yet is passed over, and read again
	// once it has one
	empty := filepath.Join(home, "projects", "-late", id(8)+".jsonl")
	os.MkdirAll(filepath.Dir(empty), 0o755)
	os.WriteFile(empty, []byte(`{"type":"mode","mode":"normal"}`+"\n"), 0o644)
	os.Chtimes(empty, now, now)
	if got := claudeDirs(home, 10); len(got) != 3 {
		t.Fatalf("an empty transcript: %+v", got)
	}
	late := mk("late")
	appendLine(t, empty, `{"type":"user","message":{"role":"user","content":"hi"},"entrypoint":"cli","cwd":"`+late+`"}`, now)
	if got := claudeDirs(home, 10); len(got) != 4 || got[0].Path != late {
		t.Fatalf("a transcript that got its first entry: %+v", got)
	}
}

func TestAgentDirsHandler(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	t.Setenv("HOME", home) // "~" in a typed path
	work := filepath.Join(home, "work")
	os.MkdirAll(work, 0o755)
	writeClaudeTranscript(t, home, "-work", "81afe071-7e89-4ee7-9522-000000000001", "cli", work, "hello", time.Now())
	s := &Server{StateDir: t.TempDir()}
	ask := func(app, query string) (int, map[string]any) {
		r := httptest.NewRequest("GET", "/v1/agents/"+app+"/dirs"+query, nil)
		r.SetPathValue("app", app)
		rec := httptest.NewRecorder()
		s.handleAgentDirs(rec, r)
		var out map[string]any
		json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	code, out := ask("claude", "")
	dirs, _ := out["dirs"].([]any)
	def := s.agentProjectDir()
	if code != http.StatusOK || out["default"] != def || len(dirs) == 0 || dirs[0].(map[string]any)["path"] != work {
		t.Fatalf("list: %d %v", code, out)
	}
	// the default folder is always offered, last with no time when unused
	if last := dirs[len(dirs)-1].(map[string]any); last["path"] != def || (def != work && last["at"].(float64) != 0) {
		t.Fatalf("the default folder: %v", dirs)
	}
	if code, _ := ask("codex", ""); code != http.StatusNotFound {
		t.Fatalf("codex: %d", code)
	}
	if code, _ := ask("nope", ""); code != http.StatusNotFound {
		t.Fatalf("no such agent: %d", code)
	}
	check := func(path string) (int, map[string]any) { return ask("claude", "?path="+url.QueryEscape(path)) }
	if code, out := check("~/work"); code != http.StatusOK || out["path"] != work {
		t.Fatalf("~/work: %d %v", code, out)
	}
	if code, out := check(work + "/"); code != http.StatusOK || out["path"] != work {
		t.Fatalf("a trailing slash: %d %v", code, out)
	}
	for path, says := range map[string]string{
		"work":                   "not a whole path",
		filepath.Join(work, "x"): "not a folder on this machine",
		"":                       "folder's path",
	} {
		if code, out := check(path); code != http.StatusBadRequest || !strings.Contains(out["error"].(string), says) {
			t.Fatalf("%q: %d %v", path, code, out)
		}
	}
}
