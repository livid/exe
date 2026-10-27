package server

import (
	"bufio"
	"exe/internal/config"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A stream open on the API ends when its session does: a passkey removed
// ends the streams it signed in and no other; a new token ends them all,
// the ones the token let in by a header too.
func TestConnsEndWithSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("EXE_HOME", home) // handleConfigPut writes config.Path()
	s := New(&config.Config{APIToken: "test-secret"}, nil, nil, "", home)
	if err := os.WriteFile(filepath.Join(home, "config.json"), []byte(`{"api_token":"test-secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// a passkey on the file, as handlePasskeyAdd would leave it
	s.pk.mu.Lock()
	s.passkeysLocked().Passkeys = []*passkey{{ID: "pk-one", Name: "Test", RP: "127.0.0.1", Created: time.Now()}}
	s.passkeysSaveLocked()
	s.pk.mu.Unlock()
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	_, port, _ := strings.Cut(strings.TrimPrefix(srv.URL, "http://127.0.0.1:"), "")
	now := time.Now()

	// stream opens /v1/ui/events the way the desk does, and gives the
	// first event; the body stays open until the daemon ends it
	type stream struct {
		name string
		res  *http.Response
		rd   *bufio.Reader
	}
	open := func(name string, hdr map[string]string) *stream {
		t.Helper()
		req, _ := http.NewRequest("GET", srv.URL+"/v1/ui/events", nil)
		req.Header.Set("Origin", srv.URL)
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		res, err := http.DefaultTransport.RoundTrip(req)
		if err != nil {
			t.Fatal(name, err)
		}
		if res.StatusCode != 200 {
			t.Fatalf("%s: %d", name, res.StatusCode)
		}
		rd := bufio.NewReader(res.Body)
		if line, err := rd.ReadString('\n'); err != nil || !strings.HasPrefix(line, "data:") {
			t.Fatalf("%s: first line %q %v", name, line, err)
		}
		return &stream{name, res, rd}
	}
	// ended says whether the stream's body ends within a second
	ended := func(st *stream) bool {
		done := make(chan bool, 1)
		go func() {
			for {
				if _, err := st.rd.ReadString('\n'); err != nil {
					done <- true
					return
				}
			}
		}()
		select {
		case <-done:
			return true
		case <-time.After(time.Second):
			return false
		}
	}
	cookie := func(via string) string {
		return "exe_session_" + port + "=" + sessionValue("test-secret", via, now)
	}
	byPasskey := open("passkey", map[string]string{"Cookie": cookie("p" + passkeyRef("pk-one"))})
	byToken := open("token session", map[string]string{"Cookie": cookie("t")})
	byHeader := open("header", map[string]string{"Authorization": "Bearer test-secret"})
	defer byPasskey.res.Body.Close()
	defer byToken.res.Body.Close()
	defer byHeader.res.Body.Close()

	del, _ := http.NewRequest("DELETE", srv.URL+"/v1/passkeys/pk-one", nil)
	del.Header.Set("Authorization", "Bearer test-secret")
	if res, err := http.DefaultClient.Do(del); err != nil || res.StatusCode != 204 {
		t.Fatalf("delete: %v %v", res, err)
	}
	if !ended(byPasskey) {
		t.Error("the removed passkey's stream stays open")
	}
	if ended(byToken) || ended(byHeader) {
		t.Error("removing a passkey ended another session's stream")
	}

	put, _ := http.NewRequest("PUT", srv.URL+"/v1/config", strings.NewReader(`{"api_token":"test-secret-2"}`))
	put.Header.Set("Authorization", "Bearer test-secret")
	res, err := http.DefaultClient.Do(put)
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("config: %v %v", res, err)
	}
	if !ended(byToken) || !ended(byHeader) {
		t.Error("a new token left a stream open")
	}
	if s.Config().APIToken != "test-secret-2" {
		t.Fatal("the token did not change")
	}
	s.conns.mu.Lock()
	n := len(s.conns.open)
	s.conns.mu.Unlock()
	if n != 0 {
		t.Errorf("%d connection(s) still registered", n)
	}
}
