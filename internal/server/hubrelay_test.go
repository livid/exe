package server

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// relayServer mounts the relay the way the daemon does, so the path
// wildcard and the GET-only pattern are part of what is tested.
func relayServer(t *testing.T) *httptest.Server {
	t.Helper()
	s := &Server{StateDir: t.TempDir()}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/hub/relay/{path...}", s.handleHubRelay)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// A read reaches the hub with its own path and query — the relay's hub
// and token parameters, and the desktop's credentials, stay behind — and
// the answer comes back boxed: no CORS grant, sandboxed, not sniffed.
func TestHubRelayCarriesAReadAndBoxesTheAnswer(t *testing.T) {
	var got *http.Request
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Clone(r.Context())
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Set-Cookie", "a=b")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"posts":[]}`))
	}))
	defer hub.Close()
	srv := relayServer(t)

	req, _ := http.NewRequest("GET", srv.URL+"/v1/hub/relay/v1/feed?limit=20&hub="+url.QueryEscape(hub.URL+"/ignored/path")+"&token=secret&before=abc", nil)
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("Cookie", "desk=1")
	req.Header.Set("Range", "bytes=0-9")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != `{"posts":[]}` {
		t.Fatalf("relayed %d %s", resp.StatusCode, body)
	}
	if got.URL.Path != "/v1/feed" || got.URL.Query().Encode() != "before=abc&limit=20" {
		t.Fatalf("hub saw %s?%s", got.URL.Path, got.URL.RawQuery)
	}
	if got.Header.Get("Authorization") != "" || got.Header.Get("Cookie") != "" {
		t.Fatalf("the desktop's credentials reached the hub: %v", got.Header)
	}
	if got.Header.Get("Range") != "bytes=0-9" {
		t.Fatalf("Range did not pass: %v", got.Header)
	}
	if resp.Header.Get("Access-Control-Allow-Origin") != "" || resp.Header.Get("Set-Cookie") != "" {
		t.Fatalf("the hub's CORS grant or cookie came through: %v", resp.Header)
	}
	if resp.Header.Get("Content-Security-Policy") != "sandbox" || resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("the answer is not boxed: %v", resp.Header)
	}
}

// /v1/events is a stream that never ends: an event must arrive while the
// hub still holds the response open.
func TestHubRelayStreamsEvents(t *testing.T) {
	release := make(chan struct{})
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"post.create\"}\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer hub.Close()
	defer close(release)
	srv := relayServer(t)

	resp, err := http.Get(srv.URL + "/v1/hub/relay/v1/events?hub=" + url.QueryEscape(hub.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	line := make(chan string, 1)
	go func() {
		l, _ := bufio.NewReader(resp.Body).ReadString('\n')
		line <- l
	}()
	select {
	case l := <-line:
		if !strings.Contains(l, "post.create") {
			t.Fatalf("first line %q", l)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the event waited for the stream to end")
	}
}

// Only a hub's read API passes: no other path, no other method, no
// address that is not a web one; a hub that is down is a 502 with the
// reason, not a hang.
func TestHubRelayRefuses(t *testing.T) {
	hits := 0
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer hub.Close()
	srv := relayServer(t)
	h := "hub=" + url.QueryEscape(hub.URL)

	for _, c := range []struct {
		method, path string
		want         int
	}{
		{"GET", "/v1/hub/relay/admin/keys?" + h, 400},
		{"GET", "/v1/hub/relay/v1?" + h, 400},
		{"GET", "/v1/hub/relay/v1/feed", 400},
		{"GET", "/v1/hub/relay/v1/feed?hub=" + url.QueryEscape("file:///etc/passwd"), 400},
		{"POST", "/v1/hub/relay/v1/msg?" + h, 405},
	} {
		req, _ := http.NewRequest(c.method, srv.URL+c.path, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != c.want {
			t.Errorf("%s %s: %d, want %d", c.method, c.path, resp.StatusCode, c.want)
		}
	}
	if hits != 0 {
		t.Fatalf("a refused request reached the hub %d time(s)", hits)
	}

	down := hub.URL
	hub.Close()
	resp, err := http.Get(srv.URL + "/v1/hub/relay/v1/hub?hub=" + url.QueryEscape(down))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 502 || !strings.Contains(string(body), "hub unreachable") {
		t.Fatalf("a hub that is down: %d %s", resp.StatusCode, body)
	}
}
