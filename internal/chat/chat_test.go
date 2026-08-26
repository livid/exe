package chat

import (
	"testing"
)

// A summary lands seconds to years after the conversation it describes;
// writing one must not reorder the session list.
func TestSetSummaryPreservesUpdatedAt(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir, "build a snake game", "test")
	if err != nil {
		t.Fatal(err)
	}
	before := s.UpdatedAt
	if err := SetSummary(dir, s.ID, "Built a snake game on port 8000."); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Summary != "Built a snake game on port 8000." {
		t.Fatalf("summary = %q", got.Summary)
	}
	if !got.UpdatedAt.Equal(before) {
		t.Fatalf("UpdatedAt changed: %v -> %v", before, got.UpdatedAt)
	}
	if metas, _ := List(dir); len(metas) != 1 || metas[0].Summary != got.Summary {
		t.Fatalf("List did not carry the summary: %+v", metas)
	}
}
