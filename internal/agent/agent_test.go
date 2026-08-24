package agent

import "testing"

// A tool call with "arguments": null must yield a writable map: JSON null
// unmarshals into a nil map with no error, and callers (pinChatArgs) write
// into the result — a nil map there panicked the daemon.
func TestParseArgsNull(t *testing.T) {
	for _, raw := range []string{"null", `"null"`, "", "{}", `{"vm":"a"}`} {
		args := ParseArgs([]byte(raw))
		if args == nil {
			t.Fatalf("ParseArgs(%q) returned nil", raw)
		}
		args["vm"] = "x" // must not panic
	}
}
