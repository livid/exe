package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"exe/internal/config"
)

// The Welcome window's check box is a file in the state directory: the
// call reads it, puts it there and takes it away, and nothing else decides
// whether the desktop opens the window by itself.
func TestWelcomeMark(t *testing.T) {
	dir := t.TempDir()
	s := New(&config.Config{}, nil, nil, "", dir)
	h := s.Handler()
	call := func(method, body string) (int, bool) {
		t.Helper()
		req := httptest.NewRequest(method, "/v1/welcome", strings.NewReader(body))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		var res struct {
			Show bool `json:"show"`
		}
		json.Unmarshal(w.Body.Bytes(), &res)
		return w.Code, res.Show
	}
	marked := func() bool {
		_, err := os.Stat(filepath.Join(dir, "welcome"))
		return err == nil
	}
	if code, show := call("GET", ""); code != http.StatusOK || show {
		t.Fatalf("a node that was never greeted: %d, show %v", code, show)
	}
	if code, show := call("PUT", `{"show":true}`); code != http.StatusOK || !show || !marked() {
		t.Fatalf("turning it on: %d, show %v, mark %v", code, show, marked())
	}
	if _, show := call("GET", ""); !show {
		t.Fatal("the mark is there and the call says it is not")
	}
	if code, show := call("PUT", `{"show":false}`); code != http.StatusOK || show || marked() {
		t.Fatalf("turning it off: %d, show %v, mark %v", code, show, marked())
	}
	// off twice is still off, and not an error
	if code, _ := call("PUT", `{"show":false}`); code != http.StatusOK {
		t.Fatalf("turning it off again: %d", code)
	}
	for _, bad := range []string{``, `{}`, `{"show":"yes"}`, `not json`} {
		if code, _ := call("PUT", bad); code != http.StatusBadRequest || marked() {
			t.Errorf("PUT %q: %d, mark %v", bad, code, marked())
		}
	}
}

// Welcome is a system app the desktop lists but keeps off its icons, and
// so are Extras, the wizard its Apps card opens, Agent Setup, and Mount
// Workspace under Help: each says so itself, and no other built-in app
// does.
func TestWelcomeIsListedHidden(t *testing.T) {
	s := New(&config.Config{}, nil, nil, "", t.TempDir())
	hidden := map[string]string{"welcome": "Welcome", "extras": "Extras", "agents": "Agent Setup", "mountworkspace": "Mount Workspace"}
	seen := false
	for _, a := range s.listApps() {
		if title, ok := hidden[a.Name]; ok {
			seen = seen || a.Name == "welcome"
			if !a.Hidden || a.Title != title || !strings.Contains(a.SystemIcon, `viewBox="0 0 32 32"`) {
				t.Errorf("%s: %+v", a.Name, a)
			}
			if a.Window == nil || a.Window.Grow {
				t.Errorf("the %s window is a fixed size: %+v", title, a.Window)
			}
			delete(hidden, a.Name)
		} else if a.Hidden {
			t.Errorf("%s is hidden from the desktop", a.Name)
		}
	}
	for name := range hidden {
		t.Errorf("no %s app among the system apps", name)
	}
	if !seen {
		t.Fatal("no welcome app among the system apps")
	}
}
