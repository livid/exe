package sshexec

import (
	"strings"
	"testing"
)

func TestReplaceEdit(t *testing.T) {
	const file = "a\nport = 8000\nb\nport = 8000\nc\n"
	tests := []struct {
		name        string
		file        string
		old, new    string
		all         bool
		want        string
		wantN       int
		errContains string
	}{
		{name: "unique match", file: file, old: "b\nport = 8000", new: "b\nport = 9000",
			want: "a\nport = 8000\nb\nport = 9000\nc\n", wantN: 1},
		{name: "replace all", file: file, old: "port = 8000", new: "port = 9000", all: true,
			want: "a\nport = 9000\nb\nport = 9000\nc\n", wantN: 2},
		{name: "ambiguous", file: file, old: "port = 8000", new: "port = 9000", errContains: "2 times"},
		{name: "not found", file: file, old: "port = 8080", new: "port = 9000", errContains: "not found"},
		{name: "empty old", file: file, old: "", new: "x", errContains: "write_file"},
		{name: "no-op", file: file, old: "a", new: "a", errContains: "identical"},

		// fallback: trailing whitespace in the file that the model dropped
		{name: "fuzzy trailing spaces", file: "a  \nport = 8000\t\nb\n",
			old: "a\nport = 8000", new: "a\nport = 9000",
			want: "a\nport = 9000\nb\n", wantN: 1},
		// fallback: CRLF file, LF old_string; the edit must stay CRLF
		{name: "fuzzy crlf", file: "a\r\nport = 8000\r\nb\r\n",
			old: "a\nport = 8000", new: "a\nport = 9000",
			want: "a\r\nport = 9000\r\nb\r\n", wantN: 1},
		{name: "fuzzy crlf single line", file: "a\r\nb\r\nc\r\n",
			old: "b", new: "B",
			want: "a\r\nB\r\nc\r\n", wantN: 1},
		{name: "fuzzy trailing newline on old", file: "a\nb\nc\n",
			old: "b \n", new: "B\n",
			want: "a\nB\nc\n", wantN: 1},
		{name: "fuzzy ambiguous", file: "x q\na\nx q\nb\n",
			old: "x q ", new: "y", errContains: "2 places"},
		{name: "fuzzy replace all", file: "x\na\nx\nb\n",
			old: "x ", new: "y", all: true,
			want: "y\na\ny\nb\n", wantN: 2},
		{name: "fuzzy last line no terminator", file: "a\nb",
			old: "b ", new: "B",
			want: "a\nB", wantN: 1},
		// an all-whitespace old_string must not fuzzy-match blank lines
		{name: "whitespace-only old", file: "a\n\n\nb\n",
			old: " \n ", new: "x", errContains: "not found"},

		// diagnostics
		{name: "over-escaped", file: "a\nb\nc\n",
			old: `a\nb`, new: "a\nB", errContains: "escape sequences"},
		{name: "near miss", file: "a\nfoo(1)\nbar(2)\nc\n",
			old: "foo(1)\nbar(3)", new: "x", errContains: "diverges at line 3"},
		{name: "nowhere", file: "a\nb\nc\n",
			old: "zzz\nqqq", new: "x", errContains: "appears nowhere"},
	}
	for _, tt := range tests {
		got, n, err := ReplaceEdit(tt.file, tt.old, tt.new, tt.all)
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

func TestNotFoundErrLargeFile(t *testing.T) {
	big := strings.Repeat("filler line\n", 2000) // > readElide bytes
	_, _, err := ReplaceEdit(big, "no such text", "x", false)
	if err == nil || !strings.Contains(err.Error(), "grep -n") {
		t.Errorf("large-file miss should point at grep -n, got: %v", err)
	}
	_, _, err = ReplaceEdit("small\n", "no such text", "x", false)
	if err == nil || !strings.Contains(err.Error(), "read_file to check") {
		t.Errorf("small-file miss should point at read_file, got: %v", err)
	}
}
