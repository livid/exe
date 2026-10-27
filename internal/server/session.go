// The desk's session cookie.
//
// The API token is a password, so the desk keeps it out of every address.
// Once a load it hands the token to the daemon (POST /v1/auth/session) and
// gets back a cookie its scripts cannot read. The browser sends that cookie
// where no header can go — an app's frame, an event stream, a terminal's
// socket, a movie — and with an app opened in a tab of its own (/apps/).
//
// The cookie holds no token: it is a MAC under a key made from the token,
// so a new token ends every session at once. It counts only on a request
// that a page of the daemon's own origin made (sameOrigin): another site,
// or another service on this host at another port, cannot spend it.
package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// sessionMaxAge is the longest a browser keeps a cookie (Chrome's limit);
// the desk asks for a new one on every load.
const sessionMaxAge = 400 * 24 * time.Hour

// sessionCookieName names the cookie after the port it was given on:
// browsers keep cookies by host, not port, so two daemons on one host (a
// scratch one beside the real one) would each overwrite the other's.
func sessionCookieName(r *http.Request) string {
	if _, port, err := net.SplitHostPort(r.Host); err == nil && port != "" {
		return "exe_session_" + port
	}
	return "exe_session"
}

func sessionMAC(tok, body string) string {
	key := hmac.New(sha256.New, []byte(tok))
	key.Write([]byte("exe session"))
	m := hmac.New(sha256.New, key.Sum(nil))
	m.Write([]byte(body))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// sessionValue is "1.<via>.<issued>.<mac>": the format, how the session
// began ("t" with the token, "p<ref>" with a passkey: passkey.go) and when,
// in Unix seconds.
func sessionValue(tok, via string, now time.Time) string {
	body := "1." + via + "." + strconv.FormatInt(now.Unix(), 10)
	return body + "." + sessionMAC(tok, body)
}

func sessionCookie(r *http.Request, tok, via string, now time.Time) *http.Cookie {
	return &http.Cookie{
		Name: sessionCookieName(r), Value: sessionValue(tok, via, now),
		Path: "/v1/", MaxAge: int(sessionMaxAge / time.Second),
		HttpOnly: true, SameSite: http.SameSiteStrictMode,
		// a fetch's POST always names its page: a desk on HTTPS (Tailscale
		// Serve, in front of a plain-HTTP daemon) gets a cookie that HTTP
		// never carries
		Secure: r.TLS != nil || strings.HasPrefix(r.Header.Get("Origin"), "https:"),
	}
}

// sessionVia is how the session cookie r carries began ("t", "p<ref>"),
// when it is one of this token's, sent for a page of the daemon's own
// origin, and "" when r carries no good one. A passkey's session lasts as
// long as the passkey is here.
func (s *Server) sessionVia(r *http.Request, tok string, now time.Time) string {
	c, err := r.Cookie(sessionCookieName(r))
	if err != nil || !sameOrigin(r) {
		return ""
	}
	i := strings.LastIndexByte(c.Value, '.')
	if i < 0 {
		return ""
	}
	body, mac := c.Value[:i], c.Value[i+1:]
	f := strings.Split(body, ".")
	if len(f) != 3 || f[0] != "1" || (f[1] != "t" && !strings.HasPrefix(f[1], "p")) {
		return ""
	}
	issued, err := strconv.ParseInt(f[2], 10, 64)
	if err != nil || now.Sub(time.Unix(issued, 0)) > sessionMaxAge {
		return ""
	}
	if !hmac.Equal([]byte(mac), []byte(sessionMAC(tok, body))) {
		return ""
	}
	if f[1] != "t" && !s.passkeyLive(f[1][1:]) {
		return ""
	}
	return f[1]
}
func (s *Server) sessionOK(r *http.Request, tok string, now time.Time) bool {
	return s.sessionVia(r, tok, now) != ""
}

// sameOrigin says whether the browser sent r for a page of the daemon's own
// origin. Sec-Fetch-Site says so where the browser sends it. A browser that
// does not (Safari before 16.4) still names the page in Origin or Referer,
// which no page can forge. A request that names no page is refused, and so
// is one the user typed into the address bar ("none"): no address the desk
// opens needs the cookie that way.
func sameOrigin(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin":
		return true
	case "":
	default:
		return false
	}
	for _, h := range []string{"Origin", "Referer"} {
		if v := r.Header.Get(h); v != "" {
			u, err := url.Parse(v)
			return err == nil && (u.Scheme == "http" || u.Scheme == "https") && strings.EqualFold(u.Host, r.Host)
		}
	}
	return false
}

// handleSession gives the desk its cookie for the token it sent, and says
// whether the request came with a good one already: a desk that opened its
// frames on a cookie it no longer had opens them again. A request let in
// by its cookie alone keeps that cookie: a passkey's session stays one.
func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	tok := s.Config().APIToken
	if tok == "" || requestToken(r) != tok {
		writeJSON(w, http.StatusOK, map[string]bool{"had": true})
		return
	}
	now := time.Now()
	had := s.sessionOK(r, tok, now)
	http.SetCookie(w, sessionCookie(r, tok, "t", now))
	writeJSON(w, http.StatusOK, map[string]bool{"had": had})
}
