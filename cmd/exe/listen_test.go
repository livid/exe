package main

import (
	"net"
	"testing"
	"time"
)

// A port the daemon we are replacing still holds is retried until it frees up.
func TestListenRetryWaitsForPortInUse(t *testing.T) {
	hold, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := hold.Addr().String()
	go func() {
		time.Sleep(300 * time.Millisecond)
		hold.Close()
	}()
	start := time.Now()
	ln, err := listenRetry(addr, 5*time.Second)
	if err != nil {
		t.Fatalf("listenRetry(%s): %v", addr, err)
	}
	ln.Close()
	if time.Since(start) < 250*time.Millisecond {
		t.Fatalf("bound %s while it was still held", addr)
	}
}

// An address that is on no interface yet (a Tailscale IP before tailscaled
// has logged in) is retried until the wait runs out, not refused at once.
func TestListenRetryWaitsForAddress(t *testing.T) {
	const addr = "192.0.2.1:0" // TEST-NET-1: never a local address
	start := time.Now()
	_, err := listenRetry(addr, 400*time.Millisecond)
	if err == nil {
		t.Fatalf("listenRetry(%s) succeeded", addr)
	}
	if !bindRetryable(err) {
		t.Fatalf("listenRetry(%s): %v is not treated as transient", addr, err)
	}
	if time.Since(start) < 400*time.Millisecond {
		t.Fatalf("gave up on %s after %s, before the wait ran out", addr, time.Since(start))
	}
}

// Anything else fails at once.
func TestListenRetryRefusesBadAddress(t *testing.T) {
	start := time.Now()
	if _, err := listenRetry("127.0.0.1:notaport", 5*time.Second); err == nil {
		t.Fatal("listenRetry(bad address) succeeded")
	}
	if time.Since(start) > time.Second {
		t.Fatalf("retried an unbindable address for %s", time.Since(start))
	}
}
