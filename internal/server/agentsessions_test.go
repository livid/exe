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
	out := "exe-claude-3:1788692008:1788718715:0:1:spark\n" +
		"exe-codex:1788562035:1788717673:1:0:exe\n" +
		"exe-claude:1788692000:1788718700:1:0:✳ Daily routine not running\n" +
		"exe-claude:1788692000:1788718700:1:0:second pane\n" +
		"exe-claude-4:1788692009:1788718716:0:0:a title: with colons\n" +
		"other:x\n"
	list := parseAgentSessions(a, out, "spark")
	if len(list) != 3 {
		t.Fatalf("got %d sessions: %+v", len(list), list)
	}
	if list[2].Name != "exe-claude-4" || list[2].Title != "a title: with colons" {
		t.Errorf("session 4 = %+v", list[2])
	}
	if list[0].Name != "exe-claude" || list[0].Number != 1 || list[0].Title != "✳ Daily routine not running" || !list[0].Attached || list[0].Bell {
		t.Errorf("session 1 = %+v", list[0])
	}
	if list[1].Name != "exe-claude-3" || list[1].Number != 3 || list[1].Title != "" || list[1].Attached || !list[1].Bell || list[1].Created != 1788692008 {
		t.Errorf("session 3 = %+v", list[1])
	}
	if got := parseAgentSessions(a, "", "spark"); got == nil || len(got) != 0 {
		t.Errorf("empty output = %#v, want an empty (not nil) list", got)
	}
}

// The real thing on a tmux server of the test's own: a client on a pty,
// a second session, the client moved between them, the column's open.
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
	sh, err := s.startAgent(a, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer sh.Close()
	var seen []byte // what the pty printed, for a failure's diagnosis
	var seenMu sync.Mutex
	go func() { // drain the pty so the client never blocks on output
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
	}()
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
	list := agentSessions(a)
	if len(list) != 1 || list[0].Number != 1 || !list[0].Attached {
		t.Fatalf("after start: %+v", list)
	}
	col := newAgentColumn(s, a, sh, nil)
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
	list = agentSessions(a)
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
}
