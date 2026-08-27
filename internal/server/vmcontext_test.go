package server

import (
	"strings"
	"testing"
)

// The memory file is replaced wholesale, capped, and removed when cleared —
// the anti-rot contract the remember tool's description promises.
func TestVMMemory(t *testing.T) {
	s := &Server{StateDir: t.TempDir()}

	if got := s.readVMMemory("demo"); got != "" {
		t.Fatalf("fresh memory = %q", got)
	}
	if err := s.writeVMMemory("demo", "app lives in ~/app, port 8000"); err != nil {
		t.Fatal(err)
	}
	if got := s.readVMMemory("demo"); got != "app lives in ~/app, port 8000" {
		t.Fatalf("memory = %q", got)
	}
	if err := s.writeVMMemory("demo", "rewritten"); err != nil {
		t.Fatal(err)
	}
	if got := s.readVMMemory("demo"); got != "rewritten" {
		t.Fatalf("memory after rewrite = %q", got)
	}
	if err := s.writeVMMemory("demo", strings.Repeat("x", memoryMax+1)); err == nil {
		t.Fatal("oversized memory accepted")
	}
	if got := s.readVMMemory("demo"); got != "rewritten" {
		t.Fatalf("memory clobbered by refused write: %q", got)
	}
	if err := s.writeVMMemory("demo", " \n"); err != nil {
		t.Fatal(err)
	}
	if got := s.readVMMemory("demo"); got != "" {
		t.Fatalf("memory after clear = %q", got)
	}
	if err := s.writeVMMemory("demo", ""); err != nil {
		t.Fatal(err) // clearing twice must not error on the missing file
	}
}
