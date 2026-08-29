package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"exe/internal/chat"
	"exe/internal/config"
)

// A run whose model never stops calling tools auto-continues at every leg
// boundary (a visible notice, where users once had to type "continue") and
// ends at the absolute cap with a visible error — never by falling off the
// end silently, which looks exactly like a finished reply.
func TestChatRunAutoContinue(t *testing.T) {
	var calls atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		fmt.Fprintln(w, `{"message":{"role":"assistant","tool_calls":[{"function":{"name":"plan","arguments":{"text":"- [ ] step"}}}]},"done":true}`)
	}))
	defer backend.Close()

	s := New(&config.Config{Ollama: config.OllamaConfig{BaseURL: backend.URL, Model: "m"}}, nil, nil, "", t.TempDir())
	sess, err := chat.New(s.chatDir(), "endless", "")
	if err != nil {
		t.Fatal(err)
	}
	run := &chatRun{cancel: func() {}}
	run.cond = sync.NewCond(&run.mu)
	s.runChatLoop(context.Background(), s.Config(), "ollama", sess, run)

	if got := int(calls.Load()); got != chatMaxTurns {
		t.Fatalf("model calls = %d, want %d", got, chatMaxTurns)
	}
	notices, errs := 0, 0
	for _, b := range run.events {
		var ev struct{ Type, Error string }
		if json.Unmarshal(b, &ev) != nil {
			continue
		}
		switch ev.Type {
		case "notice":
			notices++
		case "error":
			errs++
			if want := fmt.Sprintf("%d-turn cap", chatMaxTurns); !strings.Contains(ev.Error, want) {
				t.Fatalf("error event %q does not name the cap", ev.Error)
			}
		}
	}
	if want := chatMaxTurns/chatTurnLeg - 1; notices != want {
		t.Fatalf("notice events = %d, want %d", notices, want)
	}
	if errs != 1 {
		t.Fatalf("error events = %d, want exactly 1", errs)
	}
}

// The queue closes atomically on the loop's final empty drain, so a message
// is either injected by the loop or refused (the sender falls back to a
// normal send) — never accepted and then dropped.
func TestChatRunQueue(t *testing.T) {
	run := &chatRun{}
	run.cond = sync.NewCond(&run.mu)

	if !run.queue("a") || !run.queue("b") {
		t.Fatal("queue refused while open")
	}
	if q := run.takeQueued(false); len(q) != 2 || q[0] != "a" || q[1] != "b" {
		t.Fatalf("takeQueued = %v", q)
	}
	// a final drain that finds messages keeps the queue open: the loop
	// continues with them, so later messages may still be queued
	run.queue("c")
	if q := run.takeQueued(true); len(q) != 1 || q[0] != "c" {
		t.Fatalf("final takeQueued = %v", q)
	}
	if !run.queue("d") {
		t.Fatal("queue must stay open after a non-empty final drain")
	}
	if q := run.closeQueue(); len(q) != 1 || q[0] != "d" {
		t.Fatalf("closeQueue = %v", q)
	}
	if run.queue("e") {
		t.Fatal("queue accepted after close")
	}

	// an empty final drain closes: the loop is returning, later messages
	// must go to a fresh run
	run2 := &chatRun{}
	run2.cond = sync.NewCond(&run2.mu)
	if q := run2.takeQueued(true); len(q) != 0 {
		t.Fatalf("empty final drain = %v", q)
	}
	if run2.queue("x") {
		t.Fatal("queue accepted after an empty final drain")
	}
}

func waitForConfirm(t *testing.T, run *chatRun) {
	t.Helper()
	for i := 0; i < 200; i++ {
		run.mu.Lock()
		ok := run.confirm != nil
		run.mu.Unlock()
		if ok {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("confirmation never registered")
}

// A destructive tool call blocks on requestConfirm until the matching
// answer arrives (or the run is stopped); stale or mismatched answers are
// refused, so an old dialog can't approve a later confirmation.
func TestChatRunConfirm(t *testing.T) {
	run := &chatRun{}
	run.cond = sync.NewCond(&run.mu)
	done := make(chan string, 1)

	go func() { done <- run.requestConfirm(context.Background(), "m", "d", "Delete") }()
	waitForConfirm(t, run)
	if run.answerConfirm("999", true) {
		t.Fatal("answer with a wrong id accepted")
	}
	if !run.answerConfirm("1", true) {
		t.Fatal("matching answer refused")
	}
	if got := <-done; got != "approved" {
		t.Fatalf("outcome = %q, want approved", got)
	}

	go func() { done <- run.requestConfirm(context.Background(), "m", "d", "Delete") }()
	waitForConfirm(t, run)
	if !run.answerConfirm("2", false) {
		t.Fatal("decline refused")
	}
	if got := <-done; got != "declined" {
		t.Fatalf("outcome = %q, want declined", got)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() { done <- run.requestConfirm(ctx, "m", "d", "Delete") }()
	waitForConfirm(t, run)
	cancel()
	if got := <-done; got != "stopped" {
		t.Fatalf("outcome = %q, want stopped", got)
	}
	if run.answerConfirm("3", true) {
		t.Fatal("answer accepted after the run moved on")
	}
}
