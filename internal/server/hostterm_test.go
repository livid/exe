package server

import (
	"context"
	"errors"
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

// termLink is a test's end of a window's WebSocket: it reads the link
// for the life of the connection — the pty's bytes go by, text frames
// queue up for waitText — and done carries the read error that ends it.
type termLink struct {
	c    *websocket.Conn
	text chan string
	done chan error
}

func dialTerm(t *testing.T, ctx context.Context, url string) *termLink {
	t.Helper()
	c, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	l := &termLink{c: c, text: make(chan string, 256), done: make(chan error, 1)}
	go func() {
		for {
			typ, data, err := c.Read(ctx)
			if err != nil {
				l.done <- err
				return
			}
			if typ == websocket.MessageText {
				select {
				case l.text <- string(data):
				default:
				}
			}
		}
	}()
	return l
}

// waitText returns the first text frame holding want, within the wait.
func (l *termLink) waitText(t *testing.T, want string) string {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case s := <-l.text:
			if strings.Contains(s, want) {
				return s
			}
		case err := <-l.done:
			t.Fatalf("link ended waiting for %q: %v", want, err)
		case <-deadline:
			t.Fatalf("no frame with %q within 5s", want)
		}
	}
}

// waitClose returns the close that ends the link, within the wait.
func (l *termLink) waitClose(t *testing.T) (websocket.StatusCode, string) {
	t.Helper()
	select {
	case err := <-l.done:
		var ce websocket.CloseError
		if !errors.As(err, &ce) {
			t.Fatalf("link ended without a close frame: %v", err)
		}
		return ce.Code, ce.Reason
	case <-time.After(5 * time.Second):
		t.Fatal("link still open after 5s")
	}
	return 0, ""
}

// TestHostTerminalLink runs a shell as an agent through handleHostTerminal
// on a tmux server of the test's own: the window's pulse is answered, a
// second window of the session leaves the first attached, a client
// detached with the session still there is told "detached" (the window
// reconnects on that), the session's end "session ended" (final), and a
// link that stops answering the daemon's pings loses its tmux client.
func TestHostTerminalLink(t *testing.T) {
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
	a := hostAgent{app: "testsh", bin: "sh", title: "Shell", session: "exe-test-sh"}
	hostAgents[a.app] = a
	defer delete(hostAgents, a.app)
	s := &Server{StateDir: t.TempDir()}
	srv := httptest.NewServer(http.HandlerFunc(s.handleHostTerminal))
	defer srv.Close()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/?app=" + a.app
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	clients := func() int {
		out, _ := tmuxCmd("list-clients", "-t", "="+a.session, "-F", "#{client_pid}").Output()
		return len(strings.Fields(string(out)))
	}
	waitClients := func(want int) {
		t.Helper()
		for i := 0; i < 100; i++ {
			if clients() == want {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("%d clients on %s, want %d", clients(), a.session, want)
	}

	one := dialTerm(t, ctx, url)
	one.waitText(t, `"sessions"`)
	waitClients(1)
	if err := one.c.Write(ctx, websocket.MessageText, []byte(`{"ping":1757600000001}`)); err != nil {
		t.Fatal(err)
	}
	if got := one.waitText(t, "pong"); got != `{"pong":1757600000001}` {
		t.Fatalf("pong = %q", got)
	}

	// a second window of the session: both attached, the first still answering
	two := dialTerm(t, ctx, url)
	two.waitText(t, `"sessions"`)
	waitClients(2)
	if err := one.c.Write(ctx, websocket.MessageText, []byte(`{"ping":2}`)); err != nil {
		t.Fatal(err)
	}
	one.waitText(t, `{"pong":2}`)

	// detached by hand, the session going on: both links end "detached"
	if out, err := tmuxCmd("detach-client", "-s", "="+a.session).CombinedOutput(); err != nil {
		t.Fatalf("detach-client: %s", out)
	}
	for _, l := range []*termLink{one, two} {
		if code, reason := l.waitClose(t); code != websocket.StatusNormalClosure || reason != "detached" {
			t.Fatalf("after detach-client: close %d %q, want 1000 \"detached\"", code, reason)
		}
	}
	waitClients(0)

	// the window's reconnect lands on the same session; its end is final
	three := dialTerm(t, ctx, url)
	if got := three.waitText(t, `"sessions"`); !strings.Contains(got, `"current":"`+a.session+`"`) {
		t.Fatalf("reconnected window's first frame: %s", got)
	}
	waitClients(1)
	if out, err := tmuxCmd("kill-session", "-t", "="+a.session).CombinedOutput(); err != nil {
		t.Fatalf("kill-session: %s", out)
	}
	if code, reason := three.waitClose(t); code != websocket.StatusNormalClosure || reason != "session ended" {
		t.Fatalf("after kill-session: close %d %q, want 1000 \"session ended\"", code, reason)
	}

	// a link that stops answering pings loses its client: this one never
	// reads, so its pongs never go out
	termPingEvery, termPongWait = 200*time.Millisecond, 300*time.Millisecond
	defer func() { termPingEvery, termPongWait = 30*time.Second, 15*time.Second }()
	deaf, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer deaf.CloseNow()
	waitClients(1)
	waitClients(0)
}
