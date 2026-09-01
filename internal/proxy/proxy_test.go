package proxy

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

// TestForwardedProto: the scheme the edge reported survives the hop —
// cloudflared speaks plain HTTP to the proxy, and without this every
// published app would think it was served over http.
func TestForwardedProto(t *testing.T) {
	var seen http.Header
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Clone()
		seen.Set("Host", r.Host)
	}))
	defer backend.Close()
	p, err := New(filepath.Join(t.TempDir(), "routes.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Set("app.example.com", backend.URL); err != nil {
		t.Fatal(err)
	}
	front := httptest.NewServer(p.Handler())
	defer front.Close()

	get := func(proto string) http.Header {
		req, _ := http.NewRequest("GET", front.URL+"/x", nil)
		req.Host = "app.example.com"
		if proto != "" {
			req.Header.Set("X-Forwarded-Proto", proto)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return seen
	}
	if h := get("https"); h.Get("X-Forwarded-Proto") != "https" || h.Get("Host") != "app.example.com" {
		t.Errorf("edge scheme lost: proto=%q host=%q", h.Get("X-Forwarded-Proto"), h.Get("Host"))
	}
	if h := get(""); h.Get("X-Forwarded-Proto") != "http" {
		t.Errorf("plain request: proto=%q, want http", h.Get("X-Forwarded-Proto"))
	}
}
