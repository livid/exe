package server

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestAutostartRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if got := TakeAutostart(dir); got != nil {
		t.Fatalf("empty state dir: got %v, want nil", got)
	}
	if err := SaveAutostart(dir, []string{"web", "db"}); err != nil {
		t.Fatal(err)
	}
	if got, want := TakeAutostart(dir), []string{"web", "db"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("take: got %v, want %v", got, want)
	}
	// taken once: the record is gone
	if got := TakeAutostart(dir); got != nil {
		t.Fatalf("second take: got %v, want nil", got)
	}
	// no running VMs removes a stale record
	if err := SaveAutostart(dir, []string{"web"}); err != nil {
		t.Fatal(err)
	}
	if err := SaveAutostart(dir, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, autostartFile)); !os.IsNotExist(err) {
		t.Fatalf("record after empty save: stat err %v, want not-exist", err)
	}
}
