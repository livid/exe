package server

import (
	"sync"
	"testing"
)

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
