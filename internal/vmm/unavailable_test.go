package vmm

import (
	"context"
	"errors"
	"testing"
)

func TestUnavailableManager(t *testing.T) {
	cause := errors.New(`find Firecracker binary "firecracker": not found`)
	var m Manager = NewUnavailable(cause)
	ctx := context.Background()
	vms, err := m.List(ctx)
	if err != nil || len(vms) != 0 {
		t.Fatalf("List() = %v, %v; want an empty list", vms, err)
	}
	if _, err := m.Get(ctx, "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get() error = %v, want ErrNotFound", err)
	}
	if _, err := m.Create(ctx, Spec{Name: "x"}); !errors.Is(err, ErrNoBackend) || !errors.Is(err, cause) {
		t.Fatalf("Create() error = %v, want ErrNoBackend wrapping the cause", err)
	}
	for _, err := range []error{
		func() error { _, e := m.Start(ctx, "x"); return e }(),
		m.Stop(ctx, "x"),
		m.Delete(ctx, "x"),
	} {
		if !errors.Is(err, ErrNoBackend) {
			t.Fatalf("error = %v, want ErrNoBackend", err)
		}
	}
	// Reason drops the preamble and keeps the cause; an already-wrapped
	// error is not wrapped twice.
	if got := m.(*Unavailable).Reason(); got != cause.Error() {
		t.Fatalf("Reason() = %q, want %q", got, cause.Error())
	}
	if got := NewUnavailable(noBackend(cause)).Reason(); got != cause.Error() {
		t.Fatalf("Reason() of a wrapped error = %q, want %q", got, cause.Error())
	}
}
