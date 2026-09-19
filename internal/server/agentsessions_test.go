package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAgentSessionNames(t *testing.T) {
	a := hostAgents["claude"]
	if got := agentSessionName(a, 1); got != "exe-claude" {
		t.Fatalf("session 1 = %q", got)
	}
	if got := agentSessionName(a, 2); got != "exe-claude-2" {
		t.Fatalf("session 2 = %q", got)
	}
	for name, want := range map[string]int{
		"exe-claude": 1, "exe-claude-2": 2, "exe-claude-12": 12,
		"exe-claude-1": 0, "exe-claude-02": 0, "exe-claude-x": 0, "exe-codex": 0, "exe-claude2": 0, "": 0,
	} {
		if got := agentSessionNumber(a, name); got != want {
			t.Errorf("number of %q = %d, want %d", name, got, want)
		}
	}
}

func TestParseAgentSessions(t *testing.T) {
	a := hostAgents["claude"]
	out := "exe-claude-3:1788692008:1788718715:0:1::spark\n" +
		"exe-codex:1788562035:1788717673:1:0:1788717673:exe\n" +
		"exe-claude:1788692000:1788718700:1:0:1788718700:✳ Daily routine not running\n" +
		"exe-claude:1788692000:1788718700:1:0:1788718700:second pane\n" +
		"exe-claude-4:1788692009:1788718716:0:0:1788718600:a title: with colons\n" +
		"other:x\n"
	list := parseAgentSessions(a, out, []string{"spark", "exe"}, 1788718718)
	if len(list) != 3 {
		t.Fatalf("got %d sessions: %+v", len(list), list)
	}
	// latest first: 4 (created 1788692009), 3 (…008), then the icon's own (…000)
	if list[0].Name != "exe-claude-4" || list[0].Title != "a title: with colons" || !list[0].Working || list[0].lastAttached != 1788718600 {
		t.Errorf("session 4 = %+v", list[0])
	}
	if list[2].Name != "exe-claude" || list[2].Number != 1 || list[2].Title != "✳ Daily routine not running" || !list[2].Attached || list[2].Bell || list[2].Working || list[2].lastAttached != 1788718700 {
		t.Errorf("session 1 = %+v", list[2])
	}
	if list[1].Name != "exe-claude-3" || list[1].Number != 3 || list[1].Title != "" || list[1].Attached || !list[1].Bell || list[1].Created != 1788692008 || !list[1].Working || list[1].lastAttached != 0 {
		t.Errorf("session 3 = %+v", list[1])
	}
	if got := parseAgentSessions(a, "", []string{"spark"}, 1788718718); got == nil || len(got) != 0 {
		t.Errorf("empty output = %#v, want an empty (not nil) list", got)
	}

	// Codex titles (codexArgs): a spinner in front while a turn runs is
	// stripped and means working, however old the pane's last output; the
	// thread id before the first prompt is no title, nor is the project
	// folder's name a session started without the override shows
	c := hostAgents["codex"]
	out = "exe-codex:1788562035:1788700000:0:0:1788700000:⠹ List numbers through 40\n" +
		"exe-codex-2:1788562036:1788700000:0:1::01a0783c-153f-7ab3-b9da-5d7302aed8c7\n" +
		"exe-codex-3:1788562037:1788718716:1:0:1788718716:exe\n" +
		"exe-codex-4:1788562038:1788700000:0:0::Inspect exe-city unstaged changes\n"
	list = parseAgentSessions(c, out, []string{"spark", "exe"}, 1788718718)
	if len(list) != 4 {
		t.Fatalf("got %d codex sessions: %+v", len(list), list)
	}
	// latest first: created 38, 37, 36, 35 — the icon's own last
	if l := list[3]; l.Title != "List numbers through 40" || !l.Working || !l.spinner {
		t.Errorf("spinner row = %+v", l)
	}
	if l := list[2]; l.Title != "" || l.Working || l.spinner || !l.Bell {
		t.Errorf("thread-id row = %+v", l)
	}
	if l := list[1]; l.Title != "" || !l.Working || l.spinner || !l.Attached {
		t.Errorf("project-name row = %+v", l)
	}
	if l := list[0]; l.Title != "Inspect exe-city unstaged changes" || l.Working || l.spinner {
		t.Errorf("titled idle row = %+v", l)
	}
}

// Where a reopened window lands: the session a client was on most
// recently by tmux's stamp; the daemon's memory settles a same-second
// tie, the list's order a tie it has no memory of; a session never
// shown does not count, so an untouched agent has none.
func TestPickLastSession(t *testing.T) {
	list := []agentSession{
		{Name: "exe-claude", lastAttached: 100},
		{Name: "exe-claude-2", lastAttached: 300},
		{Name: "exe-claude-3", lastAttached: 300},
		{Name: "exe-claude-4", lastAttached: 200},
		{Name: "exe-claude-5"},
	}
	for remembered, want := range map[string]string{
		"": "exe-claude-2", "exe-claude-3": "exe-claude-3", "exe-claude-2": "exe-claude-2",
		"exe-claude-4": "exe-claude-2", "exe-claude-5": "exe-claude-2", "exe-claude-9": "exe-claude-2",
	} {
		if got := pickLastSession(list, remembered); got != want {
			t.Errorf("remembered %q: picked %q, want %q", remembered, got, want)
		}
	}
	if got := pickLastSession([]agentSession{{Name: "exe-claude"}, {Name: "exe-claude-2"}}, "exe-claude-2"); got != "" {
		t.Errorf("no session shown yet: picked %q, want none", got)
	}
	if got := pickLastSession(nil, "exe-claude"); got != "" {
		t.Errorf("no sessions: picked %q, want none", got)
	}
}

// The real thing on a tmux server of the test's own: a client on a pty,
// a second session, the client moved between them, the column's open,
// and the window closed and reopened, landing where it was.
func TestAgentSessionsLive(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("no tmux on this host")
	}
	tmuxSocket = fmt.Sprintf("exe-test-%d", os.Getpid())
	defer func() {
		if c := tmuxCmd("kill-server"); c != nil {
			c.Run()
		}
		tmuxSocket = ""
	}()
	s := &Server{StateDir: t.TempDir()}
	a := hostAgent{app: "sh", bin: "sh", title: "Shell", session: "exe-test-sh"}
	if got := s.lastAgentSession(a); got != "" {
		t.Fatalf("last session before any = %q", got)
	}
	sh, session, err := s.startAgent(a, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer sh.Close()
	if session != a.session {
		t.Fatalf("first window opens on %q, want the icon's own session", session)
	}
	var seen []byte // what the pty printed, for a failure's diagnosis
	var seenMu sync.Mutex
	drain := func(sh agentShell) { // drain the pty so the client never blocks on output
		buf := make([]byte, 4096)
		for {
			n, err := sh.Read(buf)
			if err != nil {
				return
			}
			seenMu.Lock()
			seen = append(seen, buf[:n]...)
			seenMu.Unlock()
		}
	}
	go drain(sh)
	waitFor := func(what string, ok func() bool) {
		t.Helper()
		for i := 0; i < 100; i++ {
			if ok() {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		seenMu.Lock()
		defer seenMu.Unlock()
		clients, _ := tmuxCmd("list-clients", "-F", "#{client_pid}:#{client_tty}:#{client_session}").CombinedOutput()
		t.Fatalf("timed out waiting for %s\nclients: %q\npty printed: %q", what, clients, seen)
	}
	waitFor("the client to attach", func() bool { return sh.Current() == a.session })
	// the wheel: 100 lines of output give the pane a history, and Scroll
	// walks it in tmux's copy mode — back, forward, out at the bottom
	// (-e), and 0 leaves it at once for a key typed while scrolled back
	sh.Write([]byte("seq 1 100\n"))
	view := func() string {
		out, _ := tmuxCmd("display-message", "-p", "-t", a.session, "#{history_size} #{pane_in_mode} #{scroll_position}").Output()
		return strings.TrimSpace(string(out))
	}
	waitFor("the pane to fill its history", func() bool { return !strings.HasPrefix(view(), "0 ") })
	for _, step := range []struct {
		lines int
		want  string // pane_in_mode scroll_position
	}{
		{10, "1 10"}, {5, "1 15"}, {-4, "1 11"}, {-30, "0"}, {-3, "0"}, {7, "1 7"}, {0, "0"}, {0, "0"},
	} {
		if err := sh.Scroll(step.lines); err != nil {
			t.Fatalf("Scroll(%d): %v", step.lines, err)
		}
		var got string
		for i := 0; i < 100; i++ {
			if f := strings.Fields(view()); len(f) > 1 {
				got = strings.Join(f[1:], " ")
			}
			if got == step.want {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if got != step.want {
			t.Fatalf("after Scroll(%d): mode and position %q, want %q", step.lines, got, step.want)
		}
	}
	// left scrolled back, the session comes up live when the window
	// switches back to it (below, after New moved the window away)
	if err := sh.Scroll(7); err != nil {
		t.Fatal(err)
	}
	list := s.agentSessions(a)
	if len(list) != 1 || list[0].Number != 1 || !list[0].Attached {
		t.Fatalf("after start: %+v", list)
	}
	col := newAgentColumn(s, a, sh, session, nil)
	if col.current() != a.session {
		t.Fatalf("column current = %q", col.current())
	}
	if err := col.open(); err != nil {
		t.Fatal(err)
	}
	waitFor("the client on session 2", func() bool { return sh.Current() == "exe-test-sh-2" })
	if col.current() != "exe-test-sh-2" {
		t.Fatalf("column current after open = %q", col.current())
	}
	list = s.agentSessions(a)
	if len(list) != 2 || list[0].Name != "exe-test-sh-2" || !list[0].Attached || list[1].Attached {
		t.Fatalf("after open (latest first): %+v", list)
	}
	if err := col.switchTo(a.session); err != nil {
		t.Fatal(err)
	}
	waitFor("the client back on session 1", func() bool { return sh.Current() == a.session })
	if f := strings.Fields(view()); len(f) < 2 || f[1] != "0" {
		t.Fatalf("session 1 after the switch back: history/mode/position %q, want out of copy mode", f)
	}
	if err := col.switchTo("exe-test-sh-9"); err == nil {
		t.Fatal("switching to a session that does not exist should fail")
	}
	if err := col.switchTo("other"); err == nil {
		t.Fatal("switching to a stranger's session should fail")
	}
	if err := col.open(); err != nil {
		t.Fatal(err)
	}
	waitFor("the client on session 3", func() bool { return sh.Current() == "exe-test-sh-3" })
	if got := col.statusFile(); got != s.agentStatusFile(a, "exe-test-sh-3") {
		t.Fatalf("status file = %q", got)
	}
	// archive the session on screen: the window moves to the one before it
	if err := col.archive("exe-test-sh-3"); err != nil {
		t.Fatal(err)
	}
	waitFor("the client on session 2 after the archive", func() bool { return sh.Current() == "exe-test-sh-2" })
	if list = s.agentSessions(a); len(list) != 2 || list[0].Name != "exe-test-sh-2" {
		t.Fatalf("after killing 3: %+v", list)
	}
	// close the window and open it again: it lands on session 2, where it
	// was, without touching session 1 — and so does a daemon that has
	// forgotten (a restart), by tmux's stamp alone, when the two differ
	sh.Close()
	waitFor("the client to detach with the window", func() bool { return sh.Current() == "" })
	if got := s.lastAgentSession(a); got != "exe-test-sh-2" {
		t.Fatalf("last session after closing = %q", got)
	}
	s.agentLast = nil
	if list = s.agentSessions(a); list[0].lastAttached != list[1].lastAttached {
		if got := s.lastAgentSession(a); got != "exe-test-sh-2" {
			t.Fatalf("last session by the stamp alone = %q (%+v)", got, list)
		}
	}
	s.rememberAgentSession(a, "exe-test-sh-2")
	if sh, session, err = s.startAgent(a, 80, 24); err != nil {
		t.Fatal(err)
	}
	defer sh.Close()
	go drain(sh)
	if session != "exe-test-sh-2" {
		t.Fatalf("reopened window opens on %q, want session 2", session)
	}
	waitFor("the reopened client on session 2", func() bool { return sh.Current() == "exe-test-sh-2" })
	if list = s.agentSessions(a); len(list) != 2 || !list[0].Attached || list[1].Attached {
		t.Fatalf("after reopening: %+v", list)
	}
	col = newAgentColumn(s, a, sh, session, nil)
	if col.current() != "exe-test-sh-2" {
		t.Fatalf("reopened column current = %q", col.current())
	}
	// archive one off screen: the window stays where it is
	if err := col.archive(a.session); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if list = s.agentSessions(a); len(list) != 1 || list[0].Name != "exe-test-sh-2" || sh.Current() != "exe-test-sh-2" {
		t.Fatalf("after killing 1: %+v, current %q", list, sh.Current())
	}
	if err := col.archive("other"); err == nil {
		t.Fatal("archiving a stranger's session should fail")
	}
	// the last session goes without a neighbour: the client detaches
	if err := col.archive("exe-test-sh-2"); err != nil {
		t.Fatal(err)
	}
	waitFor("the client to detach with the last session", func() bool { return sh.Current() == "" })

	// a thread started elsewhere, continued here: a Codex-like agent (notify:
	// its config overrides go on the command line) whose binary is a shell
	// that ignores them. resume starts a numbered session in the thread's own
	// folder with `resume <id>` on the line, notes the thread on the session
	// at once, and the window moves there; the thread then leaves the list
	fake := filepath.Join(t.TempDir(), "fakecodex")
	os.WriteFile(fake, []byte("#!/bin/sh\nexec sh\n"), 0o755)
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	id := "01a0755c-a0f7-7313-8d1c-e6d5385fb06f"
	dir := t.TempDir()
	writeRollout(t, home, "05", id, "codex_chatgpt_ios_remote", `"vscode"`, "", dir, "hello from the phone", time.Now())
	b := hostAgent{app: "fx", bin: fake, title: "Fake", session: "exe-test-fx", openaiUsage: true, notify: true}
	sh2, _, err := s.startAgent(b, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer sh2.Close()
	go drain(sh2)
	waitFor("the fake agent's client to attach", func() bool { return sh2.Current() == b.session })
	col2 := newAgentColumn(s, b, sh2, b.session, nil)
	if th := s.agentThreads(b, s.agentSessions(b)); len(th) != 1 || th[0].ID != id || th[0].Title != "hello from the phone" {
		t.Fatalf("threads before: %+v", th)
	}
	if err := col2.resume("not an id"); err == nil {
		t.Fatal("resume of a non-id succeeded")
	}
	if err := col2.resume(id); err != nil {
		t.Fatal(err)
	}
	waitFor("the client on the resumed session", func() bool { return sh2.Current() == "exe-test-fx-2" })
	out, _ := tmuxCmd("display-message", "-p", "-t", "exe-test-fx-2", "#{pane_start_command}\t#{pane_start_path}").Output()
	if line := string(out); !strings.Contains(line, " 'resume' '"+id+"'") || !strings.Contains(line, "\t"+dir) {
		t.Fatalf("resumed session's command line: %q", line)
	}
	if got := readAgentThreadID(s.agentStatusFile(b, "exe-test-fx-2")); got != id {
		t.Fatalf("thread noted on the session: %q", got)
	}
	if th := s.agentThreads(b, s.agentSessions(b)); len(th) != 0 {
		t.Fatalf("threads after: %+v", th)
	}
	if th := s.agentThreads(a, s.agentSessions(a)); th != nil {
		t.Fatalf("a hookless agent lists threads: %+v", th)
	}
}

// The prompt endpoint types into a live session: pasted and sent with
// Return. Its tmux target is a pane, and "=name" alone is not one
// ("can't find pane"), which sent every hub reply meant for an open
// session to a headless run instead.
func TestAgentSessionPromptLive(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("no tmux on this host")
	}
	tmuxSocket = fmt.Sprintf("exe-test-prompt-%d", os.Getpid())
	defer func() {
		if c := tmuxCmd("kill-server"); c != nil {
			c.Run()
		}
		tmuxSocket = ""
	}()
	dir := t.TempDir()
	if out, err := tmuxCmd("new-session", "-d", "-s", "exe-claude-2", "-x", "80", "-y", "24", "sh").CombinedOutput(); err != nil {
		t.Fatalf("new-session: %v %s", err, out)
	}
	typed := filepath.Join(dir, "typed")
	body, _ := json.Marshal(map[string]string{"prompt": "echo pasted > " + typed})
	s := &Server{StateDir: dir}
	prompt := func(name string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/v1/agents/claude/sessions/"+name+"/prompt", bytes.NewReader(body))
		r.SetPathValue("app", "claude")
		r.SetPathValue("name", name)
		w := httptest.NewRecorder()
		s.handleAgentSessionPrompt(w, r)
		return w
	}
	if w := prompt("exe-claude-2"); w.Code != http.StatusNoContent {
		t.Fatalf("prompt: %d %s", w.Code, w.Body)
	}
	for i := 0; ; i++ {
		if b, _ := os.ReadFile(typed); strings.TrimSpace(string(b)) == "pasted" {
			break
		}
		if i == 100 {
			pane, _ := tmuxCmd("capture-pane", "-p", "-t", "=exe-claude-2:").Output()
			t.Fatalf("the prompt never ran; pane shows %q", pane)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if w := prompt("exe-claude-3"); w.Code != http.StatusNotFound {
		t.Fatalf("prompt to a session that does not exist: %d %s, want 404", w.Code, w.Body)
	}
	// "say" goes in as keystrokes ahead of the paste, on one line: Claude
	// Code reads a paste alone as data and declines what it asks
	said := filepath.Join(dir, "said")
	body, _ = json.Marshal(map[string]string{"say": "echo typed\nfirst", "prompt": "pasted > " + said})
	if w := prompt("exe-claude-2"); w.Code != http.StatusNoContent {
		t.Fatalf("prompt with say: %d %s", w.Code, w.Body)
	}
	for i := 0; ; i++ {
		if b, _ := os.ReadFile(said); strings.TrimSpace(string(b)) == "typed first pasted" {
			break
		}
		if i == 100 {
			pane, _ := tmuxCmd("capture-pane", "-p", "-t", "=exe-claude-2:").Output()
			t.Fatalf("say and prompt never ran as one line; pane shows %q", pane)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
