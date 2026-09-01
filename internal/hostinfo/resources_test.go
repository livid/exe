package hostinfo

import "testing"

func TestBuildInfoShortens(t *testing.T) {
	// BuildInfo only reads what the toolchain stamped; in `go test` there
	// is usually no VCS stamp, so the zero value must come back clean.
	b := BuildInfo()
	if len(b.Commit) > 7 {
		t.Errorf("commit %q not shortened", b.Commit)
	}
	if len(b.Date) > 10 {
		t.Errorf("date %q not a plain day", b.Date)
	}
}

func TestProcessMemory(t *testing.T) {
	if ProcessMemory() == 0 {
		t.Error("ProcessMemory() = 0, want the runtime's footprint")
	}
}
