package server

import (
	"fmt"
	"os"
	"os/exec"
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
	if list[2].Name != "exe-claude-4" || list[2].Title != "a title: with colons" || !list[2].Working || list[2].lastAttached != 1788718600 {
		t.Errorf("session 4 = %+v", list[2])
	}
	if list[0].Name != "exe-claude" || list[0].Number != 1 || list[0].Title != "✳ Daily routine not running" || !list[0].Attached || list[0].Bell || list[0].Working || list[0].lastAttached != 1788718700 {
		t.Errorf("session 1 = %+v", list[0])
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
	if l := list[0]; l.Title != "List numbers through 40" || !l.Working || !l.spinner {
		t.Errorf("spinner row = %+v", l)
	}
	if l := list[1]; l.Title != "" || l.Working || l.spinner || !l.Bell {
		t.Errorf("thread-id row = %+v", l)
	}
	if l := list[2]; l.Title != "" || !l.Working || l.spinner || !l.Attached {
		t.Errorf("project-name row = %+v", l)
	}
	if l := list[3]; l.Title != "Inspect exe-city unstaged changes" || l.Working || l.spinner {
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
	if len(list) != 2 || list[0].Attached || !list[1].Attached {
		t.Fatalf("after open: %+v", list)
	}
	if err := col.switchTo(a.session); err != nil {
		t.Fatal(err)
	}
	waitFor("the client back on session 1", func() bool { return sh.Current() == a.session })
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
	if list = s.agentSessions(a); len(list) != 2 || list[1].Name != "exe-test-sh-2" {
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
	if list = s.agentSessions(a); len(list) != 2 || list[0].Attached || !list[1].Attached {
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
}
