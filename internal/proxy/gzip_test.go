package proxy

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// gzipFront routes app.example.com to backend and site.example.com to a
// directory holding page.html, and returns the proxy's address.
func gzipFront(t *testing.T, backend http.Handler, page string) string {
	t.Helper()
	be := httptest.NewServer(backend)
	t.Cleanup(be.Close)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "page.html"), []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := New(filepath.Join(t.TempDir(), "routes.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Set("app.example.com", be.URL); err != nil {
		t.Fatal(err)
	}
	if err := p.Set("site.example.com", Dir+dir); err != nil {
		t.Fatal(err)
	}
	front := httptest.NewServer(p.Handler())
	t.Cleanup(front.Close)
	return front.URL
}

// rawClient sees the body as it crossed the wire: Go's transport would
// otherwise ask for gzip itself and undo it.
var rawClient = &http.Client{Transport: &http.Transport{DisableCompression: true}}

func gzipGet(t *testing.T, front, method, host, path string, hdr map[string]string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest(method, front+path, nil)
	req.Host = host
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := rawClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res, body
}

func gunzip(t *testing.T, b []byte) string {
	t.Helper()
	zr, err := gzip.NewReader(strings.NewReader(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// A page of HTML a backend sends as it is goes out gzipped to a client
// that takes gzip — the tunnel's every request says it does — and as it
// was to one that does not.
func TestGzipCompressesText(t *testing.T) {
	page := "<!doctype html><title>stats</title>" + strings.Repeat("<tr><td>/docs/vms</td><td>12</td></tr>\n", 600)
	front := gzipFront(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("ETag", `"v1"`)
		io.WriteString(w, page)
	}), page)

	for _, host := range []string{"app.example.com", "site.example.com"} {
		res, body := gzipGet(t, front, "GET", host, "/page.html", map[string]string{"Accept-Encoding": "gzip"})
		if res.Header.Get("Content-Encoding") != "gzip" {
			t.Fatalf("%s: Content-Encoding %q, want gzip", host, res.Header.Get("Content-Encoding"))
		}
		if got := gunzip(t, body); got != page {
			t.Fatalf("%s: body changed in gzip (%d bytes, want %d)", host, len(got), len(page))
		}
		if len(body)*4 > len(page) {
			t.Errorf("%s: %d bytes gzipped from %d", host, len(body), len(page))
		}
		if !strings.Contains(res.Header.Get("Vary"), "Accept-Encoding") || res.Header.Get("Accept-Ranges") != "" {
			t.Errorf("%s: Vary %q, Accept-Ranges %q", host, res.Header.Get("Vary"), res.Header.Get("Accept-Ranges"))
		}
		if et := res.Header.Get("Etag"); et != "" && !strings.HasPrefix(et, "W/") {
			t.Errorf("%s: strong ETag %q on a gzipped body", host, et)
		}
	}
	res, body := gzipGet(t, front, "GET", "app.example.com", "/", nil)
	if res.Header.Get("Content-Encoding") != "" || string(body) != page || res.Header.Get("Etag") != `"v1"` {
		t.Fatalf("no Accept-Encoding: encoding %q, %d bytes, ETag %q", res.Header.Get("Content-Encoding"), len(body), res.Header.Get("Etag"))
	}
}

// What gzip would break or not shrink passes through byte for byte.
func TestGzipLeavesAlone(t *testing.T) {
	page := strings.Repeat("<p>a line of the page</p>\n", 400)
	front := gzipFront(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/small":
			w.Header().Set("Content-Type", "text/html")
			io.WriteString(w, "<p>short</p>")
		case "/png":
			w.Header().Set("Content-Type", "image/png")
			io.WriteString(w, page)
		case "/encoded":
			w.Header().Set("Content-Type", "text/html")
			w.Header().Set("Content-Encoding", "br")
			io.WriteString(w, page)
		case "/events":
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, page)
		case "/no-transform":
			w.Header().Set("Content-Type", "text/html")
			w.Header().Set("Cache-Control", "no-transform")
			io.WriteString(w, page)
		}
	}), page)
	gz := map[string]string{"Accept-Encoding": "gzip"}
	for _, c := range []struct {
		name, method, host, path string
		hdr                      map[string]string
		code                     int
		want                     string
	}{
		{"small", "GET", "app.example.com", "/small", gz, 200, "<p>short</p>"},
		{"image", "GET", "app.example.com", "/png", gz, 200, page},
		{"already encoded", "GET", "app.example.com", "/encoded", gz, 200, page},
		{"server-sent events", "GET", "app.example.com", "/events", gz, 200, page},
		{"no-transform", "GET", "app.example.com", "/no-transform", gz, 200, page},
		{"refused by q=0", "GET", "app.example.com", "/png", map[string]string{"Accept-Encoding": "gzip;q=0"}, 200, page},
		{"byte range", "GET", "site.example.com", "/page.html", map[string]string{"Accept-Encoding": "gzip", "Range": "bytes=0-99"}, 206, page[:100]},
		{"head", "HEAD", "site.example.com", "/page.html", gz, 200, ""},
	} {
		res, body := gzipGet(t, front, c.method, c.host, c.path, c.hdr)
		if res.StatusCode != c.code || string(body) != c.want {
			t.Errorf("%s: %d, %d bytes; want %d, %d bytes", c.name, res.StatusCode, len(body), c.code, len(c.want))
		}
		if enc := res.Header.Get("Content-Encoding"); enc == "gzip" {
			t.Errorf("%s: gzipped", c.name)
		}
	}
}

// A body of unknown length that streams still arrives as it is written:
// ReverseProxy flushes after every read, and the flush reaches the client
// through gzip.
func TestGzipStreams(t *testing.T) {
	first := strings.Repeat("<li>first part</li>\n", 100)
	release := make(chan struct{})
	front := gzipFront(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, first)
		w.(http.Flusher).Flush()
		<-release
		io.WriteString(w, "<li>the rest</li>")
	}), "")
	defer close(release)

	req, _ := http.NewRequest("GET", front+"/", nil)
	req.Host = "app.example.com"
	req.Header.Set("Accept-Encoding", "gzip")
	res, err := rawClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.Header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("Content-Encoding %q", res.Header.Get("Content-Encoding"))
	}
	got := make(chan string, 1)
	go func() {
		zr, err := gzip.NewReader(res.Body)
		if err != nil {
			got <- err.Error()
			return
		}
		b := make([]byte, len(first))
		n, _ := io.ReadFull(zr, b)
		got <- string(b[:n])
	}()
	select {
	case s := <-got:
		if s != first {
			t.Fatalf("first part: %d bytes, want %d", len(s), len(first))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the first part was held back until the body ended")
	}
}
