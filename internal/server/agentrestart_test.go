package server

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A host restart on a tmux server of the test's own: sessions noted while
// the daemon runs, the server killed as the host's going down kills it,
// and the daemon's next start under another boot. The CLI is a script
// that logs how it was launched and waits.
func TestHostRestartLive(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("no tmux on this host")
	}
	tmuxSocket = fmt.Sprintf("exe-test-restart-%d", os.Getpid())
	killServer := func() {
		if c := tmuxCmd("kill-server"); c != nil {
			c.Run()
		}
	}
	defer func() { killServer(); tmuxSocket = "" }()

	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	work := t.TempDir() // the folder the stopped conversation was started in
	bin := t.TempDir()
	launches := filepath.Join(bin, "launches")
	cli := filepath.Join(bin, "fakecli")
	script := "#!/bin/sh\nprintf '%s|%s\\n' \"$(pwd)\" \"$*\" >> " + shQuote(launches) + "\nexec sleep 600\n"
	if err := os.WriteFile(cli, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	a := hostAgent{app: "fake", bin: cli, title: "Fake", session: "exe-fake"}
	agents := []hostAgent{a}
	s := &Server{StateDir: t.TempDir()}
	os.MkdirAll(filepath.Join(s.StateDir, "agents"), 0o755) // a CLI with no hooks makes no status folder

	// a conversation with a transcript, one without (no prompt yet), and
	// a third whose session ends while the daemon watches
	const (
		id2 = "11111111-2222-4333-8444-555555555555"
		id3 = "22222222-3333-4444-8555-666666666666"
		id4 = "33333333-4444-4555-8666-777777777777"
	)
	transcript := func(id string) string {
		dir := filepath.Join(home, "projects", "-work")
		os.MkdirAll(dir, 0o755)
		path := filepath.Join(dir, id+".jsonl")
		line, _ := json.Marshal(map[string]any{"type": "user", "cwd": work, "sessionId": id, "timestamp": "2026-10-27T01:00:00Z",
			"message": map[string]any{"role": "user", "content": "carry on"}})
		if err := os.WriteFile(path, append(line, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	for name, id := range map[string]string{"exe-fake-2": id2, "exe-fake-3": id3, "exe-fake-4": id4} {
		if err := s.newAgentSession(a, name); err != nil {
			t.Fatal(err)
		}
		status := map[string]string{"session_id": id}
		if id != id3 {
			status["transcript_path"] = transcript(id)
		}
		b, _ := json.Marshal(status)
		os.WriteFile(s.agentStatusFile(a, name), b, 0o644)
	}
	readNote := func() agentLiveNote {
		t.Helper()
		var n agentLiveNote
		b, err := os.ReadFile(s.agentLiveFile())
		if err != nil || json.Unmarshal(b, &n) != nil {
			t.Fatalf("live.json: %v %q", err, b)
		}
		return n
	}
	noted := func(n agentLiveNote) []string {
		var names []string
		for _, x := range n.Sessions["fake"] {
			names = append(names, x.Name)
		}
		return names
	}

	last := s.noteAgentSessions(agents, "boot-1", nil)
	if got := noted(readNote()); len(got) != 3 {
		t.Fatalf("noted %v, want the three sessions", got)
	}
	if again := s.noteAgentSessions(agents, "boot-1", last); string(again) != string(last) {
		t.Fatal("an unchanged list wrote the note again")
	}
	// session 4's CLI ends while the daemon watches: it leaves the note
	tmuxCmd("kill-session", "-t", "=exe-fake-4").Run()
	last = s.noteAgentSessions(agents, "boot-1", last)
	if got := noted(readNote()); len(got) != 2 || strings.Contains(strings.Join(got, ","), "exe-fake-4") {
		t.Fatalf("noted %v after session 4 ended", got)
	}

	// the host goes down: the server first, and the note must stand
	killServer()
	s.noteAgentSessions(agents, "boot-1", last)
	if got := noted(readNote()); len(got) != 2 {
		t.Fatalf("a pass with no server rewrote the note: %v", got)
	}

	// the same boot (a daemon restart) stops nothing
	s.noteHostRestart(agents, "boot-1")
	if st := s.stoppedSessions(a); len(st) != 0 {
		t.Fatalf("a daemon restart stopped %+v", st)
	}

	// the next boot: session 2 is stopped; 3 had no conversation, 4 ended before
	s.noteHostRestart(agents, "boot-2")
	st := s.stoppedSessions(a)
	if len(st) != 1 || st[0].Name != "exe-fake-2" || st[0].Number != 2 || st[0].Thread != id2 || st[0].Created == 0 {
		t.Fatalf("stopped %+v, want session 2 alone", st)
	}
	if n := readNote(); n.Boot != "boot-2" || len(n.Sessions["fake"]) != 0 {
		t.Fatalf("the note after the restart = %+v, want boot-2 and nothing live", n)
	}
	s.noteHostRestart(agents, "boot-2") // told once: a second start in the boot adds nothing
	if st := s.stoppedSessions(a); len(st) != 1 {
		t.Fatalf("the restart was taken in twice: %+v", st)
	}
	// a fresh daemon reads the file
	s = &Server{StateDir: s.StateDir}
	rows := s.withStopped(a, s.agentSessions(a))
	if len(rows) != 1 || !rows[0].Stopped || !rows[0].Resumable || rows[0].Name != "exe-fake-2" {
		t.Fatalf("rows %+v, want session 2 stopped", rows)
	}
	if got := s.nextAgentSession(a); got != "exe-fake-3" {
		t.Fatalf("next session %q: a stopped session's number is taken", got)
	}

	// a window opens with nothing running: it lands on session 2, continued
	sh, session, err := s.startAgent(a, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	go io.Copy(io.Discard, sh)
	if session != "exe-fake-2" {
		t.Fatalf("the window opened on %q, want the stopped session continued", session)
	}
	var got string
	for i := 0; i < 100 && !strings.Contains(got, id2); i++ {
		time.Sleep(50 * time.Millisecond)
		b, _ := os.ReadFile(launches)
		got = string(b)
	}
	if want := work + "|--resume " + id2; !strings.Contains(got, want) {
		t.Fatalf("launches %q, want %q", got, want)
	}
	sh.Close()
	if id := readAgentThreadID(s.agentStatusFile(a, "exe-fake-2")); id != id2 {
		t.Fatalf("the continued session's status names %q", id)
	}
	if st := s.stoppedSessions(a); len(st) != 0 {
		t.Fatalf("still stopped after it was continued: %+v", st)
	}
	if _, err := os.Stat(s.agentStoppedFile()); !os.IsNotExist(err) {
		t.Fatalf("stopped.json outlived the last stopped session: %v", err)
	}

	// another restart, and Archive on the row lets it go
	s.noteAgentSessions(agents, "boot-2", nil)
	killServer()
	s.noteHostRestart(agents, "boot-3")
	if st := s.stoppedSessions(a); len(st) != 1 {
		t.Fatalf("stopped after the second restart: %+v", st)
	}
	if !s.forgetStopped(a, "exe-fake-2") || s.forgetStopped(a, "exe-fake-2") {
		t.Fatal("forgetStopped: the row should go once")
	}
	if _, err := os.Stat(s.agentStatusFile(a, "exe-fake-2")); !os.IsNotExist(err) {
		t.Fatalf("the archived row's status file stayed: %v", err)
	}
	if rows := s.withStopped(a, nil); len(rows) != 0 {
		t.Fatalf("rows after Archive: %+v", rows)
	}

	// a conversation gone from the machine since: the switch fails and the row goes
	status, _ := json.Marshal(map[string]string{"session_id": id4})
	os.WriteFile(s.agentStatusFile(a, "exe-fake-4"), status, 0o644)
	note, _ := json.Marshal(agentLiveNote{Boot: "boot-3", Sessions: map[string][]agentNote{"fake": {{Name: "exe-fake-4", Number: 4, Created: 1}}}})
	os.WriteFile(s.agentLiveFile(), note, 0o644)
	s.noteHostRestart(agents, "boot-4")
	os.Remove(filepath.Join(home, "projects", "-work", id4+".jsonl"))
	if woke, err := s.wakeAgentSession(a, "exe-fake-4"); woke || err == nil {
		t.Fatalf("woke a conversation that is gone: %v %v", woke, err)
	}
	if st := s.stoppedSessions(a); len(st) != 0 {
		t.Fatalf("a gone conversation kept its row: %+v", st)
	}
}

// Archive takes a session out of the note at once.
func TestUnnoteAgentSession(t *testing.T) {
	s := &Server{StateDir: t.TempDir()}
	a := hostAgents["claude"]
	note, _ := json.Marshal(agentLiveNote{Boot: "b", Sessions: map[string][]agentNote{
		"claude": {{Name: "exe-claude-2", Number: 2}, {Name: "exe-claude-3", Number: 3}},
		"codex":  {{Name: "exe-claude-2", Number: 2}},
	}})
	os.MkdirAll(filepath.Dir(s.agentLiveFile()), 0o755)
	os.WriteFile(s.agentLiveFile(), note, 0o644)
	s.unnoteAgentSession(a, "exe-claude-2")
	var n agentLiveNote
	b, _ := os.ReadFile(s.agentLiveFile())
	json.Unmarshal(b, &n)
	if len(n.Sessions["claude"]) != 1 || n.Sessions["claude"][0].Name != "exe-claude-3" || len(n.Sessions["codex"]) != 1 || n.Boot != "b" {
		t.Fatalf("note after unnote = %+v", n)
	}
}
