package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestTermSessionNumber(t *testing.T) {
	for name, want := range map[string]int{
		"exe-term-1": 1, "exe-term-12": 12,
		"exe-term-0": 0, "exe-term-01": 0, "exe-term-": 0, "exe-term-x": 0, "exe-term--1": 0,
		"exe-claude": 0, "exe-claude-2": 0, "exe-terminal-2": 0,
	} {
		if got := termSessionNumber(name); got != want {
			t.Errorf("termSessionNumber(%q) = %d, want %d", name, got, want)
		}
	}
	if got := termSessionName(3); got != "exe-term-3" {
		t.Errorf("termSessionName(3) = %q", got)
	}
}

func TestTermQuerySize(t *testing.T) {
	for q, want := range map[string][2]int{
		"cols=120&rows=40": {120, 40}, "": {80, 24}, "cols=120": {80, 24},
		"cols=1&rows=40": {80, 24}, "cols=120&rows=1001": {80, 24}, "cols=x&rows=40": {80, 24},
	} {
		r := httptest.NewRequest("GET", "/v1/host/terminal?term=1&"+q, nil)
		if c, rr := termQuerySize(r); c != want[0] || rr != want[1] {
			t.Errorf("termQuerySize(%q) = %d×%d, want %d×%d", q, c, rr, want[0], want[1])
		}
	}
}

func TestParseTermSessions(t *testing.T) {
	out := "exe-claude:1757600000:1\nexe-term-10:1757600300:0\nexe-term-2:1757600200:1\nexe-term-x:1:0\nmine:1:0\n"
	got := parseTermSessions(out)
	if len(got) != 2 || got[0].Name != "exe-term-2" || got[0].Number != 2 || !got[0].Attached || got[0].Created != 1757600200 ||
		got[1].Name != "exe-term-10" || got[1].Attached {
		t.Fatalf("parseTermSessions = %+v", got)
	}
}

// TestTermSessionsLive runs Terminal sessions on a tmux server of the
// test's own, through the routes the desk uses: POST starts one under
// the lowest free number with tmux out of sight, a window's link
// attaches with ?term=n, and ending the link leaves the shell and its
// screen for the next link. A second window of it hears "closed" when
// the close box (DELETE) ends it, a hang-up by hand "detached", and the
// shell's exit "session ended" — which is also what a link to a session
// that is not there hears at once. Every desk hears of a session
// started, naming the desk that asked, and a link starts at the size it
// asks for.
func TestTermSessionsLive(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("no tmux on this host")
	}
	tmuxSocket = fmt.Sprintf("exe-test-%d", os.Getpid())
	defer func() {
		if c := tmuxCmd("kill-server"); c != nil {
			c.Run()
		}
		tmuxSocket = ""
	}()
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("HOME", t.TempDir()) // no profile of the user's runs in the test's shells
	s := &Server{StateDir: t.TempDir()}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/host/terminal", s.handleHostTerminal)
	mux.HandleFunc("GET /v1/host/terminals", s.handleTermSessions)
	mux.HandleFunc("POST /v1/host/terminals", s.handleTermSessionNew)
	mux.HandleFunc("DELETE /v1/host/terminals/{n}", s.handleTermSessionEnd)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	call := func(method, path string) (int, map[string]any) {
		t.Helper()
		req, _ := http.NewRequest(method, srv.URL+path, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var v map[string]any
		json.NewDecoder(resp.Body).Decode(&v)
		return resp.StatusCode, v
	}
	// the layout stream, as a desk subscribed to it hears it
	events := make(chan []byte, 8)
	s.ui.subs = map[chan []byte]struct{}{events: {}}
	create := func(want int) {
		t.Helper()
		req, _ := http.NewRequest("POST", srv.URL+"/v1/host/terminals", strings.NewReader(`{"client":"desk-a"}`))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var v map[string]any
		json.NewDecoder(resp.Body).Decode(&v)
		resp.Body.Close()
		if resp.StatusCode != http.StatusCreated || v["number"] != float64(want) || v["name"] != termSessionName(want) {
			t.Fatalf("POST: %d %v, want number %d", resp.StatusCode, v, want)
		}
		select {
		case b := <-events:
			var ev struct {
				Term   termSession `json:"term"`
				Client string      `json:"client"`
				State  any         `json:"state"`
			}
			if json.Unmarshal(b, &ev) != nil || ev.Term.Number != want || ev.Client != "desk-a" || ev.State != nil {
				t.Fatalf("the layout stream heard %s, want Terminal %d from desk-a and no layout", b, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("the layout stream heard nothing of Terminal %d", want)
		}
	}
	link := func(n int) *termLink {
		return dialTerm(t, ctx, fmt.Sprintf("ws%s/v1/host/terminal?term=%d", strings.TrimPrefix(srv.URL, "http"), n))
	}
	pane := func(n int) string {
		out, _ := tmuxCmd("capture-pane", "-p", "-t", termSessionName(n)).Output()
		return string(out)
	}
	waitFor := func(what string, ok func() bool) {
		t.Helper()
		for i := 0; i < 100; i++ {
			if ok() {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for %s\npane 1: %q", what, pane(1))
	}
	attached := func(n int) bool {
		for _, ts := range s.termSessions() {
			if ts.Number == n {
				return ts.Attached
			}
		}
		return false
	}
	closeWant := func(l *termLink, want string) {
		t.Helper()
		if code, reason := l.waitClose(t); code != websocket.StatusNormalClosure || reason != want {
			t.Fatalf("close %d %q, want 1000 %q", code, reason, want)
		}
	}

	if _, v := call("GET", "/v1/host/terminals"); fmt.Sprint(v["terminals"]) != "[]" {
		t.Fatalf("before any: %v", v)
	}
	create(1)
	create(2)
	for option, want := range map[string]string{"status": "off", "prefix": "None", "prefix2": "None"} {
		out, _ := tmuxCmd("show-options", "-v", "-t", "exe-term-1", option).Output()
		if got := strings.TrimSpace(string(out)); got != want {
			t.Errorf("exe-term-1 %s = %q, want %q", option, got, want)
		}
	}

	// a window: its keys reach the shell, which has no TMUX of its own
	one := link(1)
	waitFor("the window to attach", func() bool { return attached(1) })
	if err := one.c.Write(ctx, websocket.MessageBinary, []byte("echo \"mark-$((6*7)) tmux=[$TMUX]\"\n")); err != nil {
		t.Fatal(err)
	}
	waitFor("the shell to answer", func() bool { return strings.Contains(pane(1), "mark-42 tmux=[]") })

	// the link goes (a reload, a closed browser): the shell stays, and so
	// does its screen, for the next link
	one.c.Close(websocket.StatusGoingAway, "")
	waitFor("the client to detach", func() bool { return !attached(1) })
	if _, v := call("GET", "/v1/host/terminals"); !strings.Contains(fmt.Sprint(v["terminals"]), "exe-term-1") {
		t.Fatalf("after the link went: %v", v)
	}
	two := link(1)
	three := dialTerm(t, ctx, fmt.Sprintf("ws%s/v1/host/terminal?term=1&cols=101&rows=31", strings.TrimPrefix(srv.URL, "http")))
	waitFor("two windows on it", func() bool {
		out, _ := tmuxCmd("list-clients", "-t", "=exe-term-1", "-F", "#{client_pid}").Output()
		return len(strings.Fields(string(out))) == 2
	})
	// the second came in at the size it asked for, which the session took
	waitFor("the window's size", func() bool {
		out, _ := tmuxCmd("list-clients", "-t", "=exe-term-1", "-F", "#{client_width}x#{client_height}").Output()
		return strings.Contains(string(out), "101x31")
	})
	if !strings.Contains(pane(1), "mark-42") {
		t.Fatalf("the screen did not outlive the link: %q", pane(1))
	}

	// a hang-up by hand: both windows hear "detached", and reconnect
	if out, err := tmuxCmd("detach-client", "-s", "=exe-term-1").CombinedOutput(); err != nil {
		t.Fatalf("detach-client: %s", out)
	}
	closeWant(two, "detached")
	closeWant(three, "detached")

	// the close box: every window of it hears "closed"
	four, five := link(1), link(1)
	waitFor("the windows to attach", func() bool { return attached(1) })
	if code, v := call("DELETE", "/v1/host/terminals/1"); code != http.StatusOK {
		t.Fatalf("DELETE: %d %v", code, v)
	}
	closeWant(four, "closed")
	closeWant(five, "closed")
	closeWant(link(1), "closed") // a restore that raced the close
	if code, _ := call("DELETE", "/v1/host/terminals/1"); code != http.StatusNotFound {
		t.Fatalf("DELETE of an ended session: %d, want 404", code)
	}
	if code, _ := call("DELETE", "/v1/host/terminals/x"); code != http.StatusNotFound {
		t.Fatalf("DELETE of no number: %d, want 404", code)
	}

	// the lowest free number comes back, as a fresh shell whose exit is
	// "session ended", not the old session's "closed"
	create(1)
	six := link(1)
	waitFor("the window to attach", func() bool { return attached(1) })
	six.c.Write(ctx, websocket.MessageBinary, []byte("exit\n"))
	closeWant(six, "session ended")
	closeWant(link(1), "session ended")
	closeWant(link(7), "session ended")
	if list := s.termSessions(); len(list) != 1 || list[0].Name != "exe-term-2" {
		t.Fatalf("after the exit: %+v, want exe-term-2 alone", list)
	}
	create(1)
}
