package sshexec

import (
	"strings"
	"testing"
)

func TestReplaceEdit(t *testing.T) {
	const file = "a\nport = 8000\nb\nport = 8000\nc\n"
	tests := []struct {
		name        string
		old, new    string
		all         bool
		want        string
		wantN       int
		errContains string
	}{
		{name: "unique match", old: "b\nport = 8000", new: "b\nport = 9000",
			want: "a\nport = 8000\nb\nport = 9000\nc\n", wantN: 1},
		{name: "replace all", old: "port = 8000", new: "port = 9000", all: true,
			want: "a\nport = 9000\nb\nport = 9000\nc\n", wantN: 2},
		{name: "ambiguous", old: "port = 8000", new: "port = 9000", errContains: "2 times"},
		{name: "not found", old: "port = 8080", new: "port = 9000", errContains: "not found"},
		{name: "empty old", old: "", new: "x", errContains: "write_file"},
		{name: "no-op", old: "a", new: "a", errContains: "identical"},
	}
	for _, tt := range tests {
		got, n, err := ReplaceEdit(file, tt.old, tt.new, tt.all)
		if tt.errContains != "" {
			if err == nil || !strings.Contains(err.Error(), tt.errContains) {
				t.Errorf("%s: err = %v, want containing %q", tt.name, err, tt.errContains)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: unexpected error: %v", tt.name, err)
			continue
		}
		if got != tt.want || n != tt.wantN {
			t.Errorf("%s: got (%q, %d), want (%q, %d)", tt.name, got, n, tt.want, tt.wantN)
		}
	}
}
