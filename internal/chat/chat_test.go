package chat

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTitle(t *testing.T) {
	if got := Title("  build   a\nsnake game "); got != "build a snake game" {
		t.Fatalf("got %q", got)
	}
	if got := Title(""); got != "New chat" {
		t.Fatalf("got %q", got)
	}
	long := Title(strings.Repeat("word ", 100))
	if n := len([]rune(long)); n > titleRunes+1 || !strings.HasSuffix(long, "…") {
		t.Fatalf("not capped: %d runes, %q…", n, long[:20])
	}
	if strings.Contains(long, "wor…") {
		t.Fatalf("cut mid-word: %q", long)
	}
	// a cut through multi-byte text must stay valid UTF-8
	cjk := Title(strings.Repeat("汉", 200))
	if !utf8.ValidString(cjk) || len([]rune(cjk)) != titleRunes+1 {
		t.Fatalf("bad rune cut: %d runes, valid=%v", len([]rune(cjk)), utf8.ValidString(cjk))
	}
}

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
