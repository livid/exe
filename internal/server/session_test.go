package server

import (
	"encoding/json"
	"exe/internal/config"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// sessionStart is the desk's POST /v1/auth/session: the token in the
// header, the page in Origin and Sec-Fetch-Site.
func sessionStart(t *testing.T, h http.Handler, origin string, cookie *http.Cookie) (*http.Cookie, bool) {
	t.Helper()
	r := httptest.NewRequest("POST", origin+"/v1/auth/session", nil)
	r.Header.Set("Authorization", "Bearer test-secret")
	r.Header.Set("Origin", origin)
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("session = %d %s", w.Code, w.Body)
	}
	var got struct{ Had bool }
	json.Unmarshal(w.Body.Bytes(), &got)
	cs := w.Result().Cookies()
	if len(cs) != 1 {
		t.Fatalf("session set %d cookies", len(cs))
	}
	return cs[0], got.Had
}

func TestSessionCookie(t *testing.T) {
	s := New(&config.Config{APIToken: "test-secret"}, nil, nil, "", t.TempDir())
	h := s.Handler()
	c, had := sessionStart(t, h, "http://exe.test:7797", nil)
	if had {
		t.Fatal("a first session says it had one")
	}
	if c.Name != "exe_session_7797" || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode ||
		c.Path != "/v1/" || c.Secure || c.MaxAge < 300*24*3600 {
		t.Fatalf("cookie %+v", c)
	}
	if strings.Contains(c.Value, "test-secret") {
		t.Fatal("the cookie holds the token")
	}
	if _, had := sessionStart(t, h, "http://exe.test:7797", c); !had {
		t.Fatal("a second session does not see the first")
	}
	if c, _ := sessionStart(t, h, "https://exe.test", nil); c.Name != "exe_session" || !c.Secure {
		t.Fatalf("an HTTPS desk's cookie %+v", c)
	}

	// what the cookie alone gets: a request of the desk's own origin
	ask := func(cookie *http.Cookie, hdr map[string]string) int {
		r := httptest.NewRequest("GET", "http://exe.test:7797/v1/auth", nil)
		if cookie != nil {
			r.AddCookie(cookie)
		}
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	for _, tc := range []struct {
		name string
		hdr  map[string]string
		want int
	}{
		{"same origin", map[string]string{"Sec-Fetch-Site": "same-origin"}, 204},
		{"another port", map[string]string{"Sec-Fetch-Site": "same-site"}, 401},
		{"another site", map[string]string{"Sec-Fetch-Site": "cross-site"}, 401},
		{"typed address", map[string]string{"Sec-Fetch-Site": "none"}, 401},
		{"old Safari, Origin", map[string]string{"Origin": "http://exe.test:7797"}, 204},
		{"old Safari, Referer", map[string]string{"Referer": "http://exe.test:7797/apps/Notes/"}, 204},
		{"old Safari, another port", map[string]string{"Origin": "http://exe.test:7788"}, 401},
		{"old Safari, null origin", map[string]string{"Origin": "null", "Referer": "http://exe.test:7797/"}, 401},
		{"no page named", nil, 401},
	} {
		if got := ask(c, tc.hdr); got != tc.want {
			t.Errorf("%s: %d, want %d", tc.name, got, tc.want)
		}
	}
	same := map[string]string{"Sec-Fetch-Site": "same-origin"}
	if got := ask(nil, same); got != 401 {
		t.Errorf("no cookie: %d", got)
	}
	bad := *c
	bad.Value = c.Value[:len(c.Value)-2] + "xx"
	if got := ask(&bad, same); got != 401 {
		t.Errorf("a forged MAC: %d", got)
	}
	old := *c
	old.Value = sessionValue("test-secret", "t", time.Now().Add(-sessionMaxAge-time.Hour))
	if got := ask(&old, same); got != 401 {
		t.Errorf("an expired session: %d", got)
	}
	other := *c
	other.Value = sessionValue("another-token", "t", time.Now())
	if got := ask(&other, same); got != 401 {
		t.Errorf("a session of another token: %d", got)
	}
	// the token still works by itself, as curl and the agents send it
	if w := appRequest(h, "GET", "/v1/auth", "", 0); w.Code != 204 {
		t.Errorf("the token alone: %d", w.Code)
	}
}

// A daemon with no token has no session to give, and says the desk has one.
func TestSessionWithoutToken(t *testing.T) {
	s := New(&config.Config{}, nil, nil, "", t.TempDir())
	r := httptest.NewRequest("POST", "http://exe.test/v1/auth/session", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 200 || len(w.Result().Cookies()) != 0 || !strings.Contains(w.Body.String(), `"had":true`) {
		t.Fatalf("session without a token = %d %v %s", w.Code, w.Result().Cookies(), w.Body)
	}
}
