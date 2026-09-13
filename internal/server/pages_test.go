package server

import (
	"encoding/json"
	"exe/internal/config"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPageTicketServesWorkspacePageSandboxed(t *testing.T) {
	dir := t.TempDir()
	srv := New(&config.Config{APIToken: "tok"}, nil, nil, "", dir)
	const body = "<!doctype html><title>A</title><a href=\"#x\">x</a><h1 id=\"x\">X</h1>"
	if err := os.MkdirAll(filepath.Join(dir, "workspace", "Artifacts"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "workspace", "Artifacts", "A page.html"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	mint := func(path, token string) (int, map[string]any) {
		req, _ := http.NewRequest("POST", ts.URL+"/v1/pages", strings.NewReader(`{"path":`+jsonStr(path)+`}`))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var out map[string]any
		json.NewDecoder(res.Body).Decode(&out)
		return res.StatusCode, out
	}
	if code, _ := mint("Artifacts/A page.html", ""); code != http.StatusUnauthorized {
		t.Fatalf("mint without token: %d", code)
	}
	for _, bad := range []struct {
		path string
		code int
	}{{"Artifacts/notes.txt", 400}, {"../A page.html", 400}, {"Artifacts/missing.html", 404}, {"Artifacts", 400}} {
		if code, _ := mint(bad.path, "tok"); code != bad.code {
			t.Errorf("mint %q: %d, want %d", bad.path, code, bad.code)
		}
	}
	code, out := mint("Artifacts/A page.html", "tok")
	if code != 200 {
		t.Fatalf("mint: %d %v", code, out)
	}
	u, _ := out["url"].(string)
	if !strings.HasPrefix(u, "/pages/") || !strings.HasSuffix(u, "/A%20page.html") || out["size"] != float64(len(body)) {
		t.Fatalf("mint answer %v", out)
	}

	// the page comes without a token, as text/html, under a CSP sandbox
	res, err := http.Get(ts.URL + u)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || string(got) != body {
		t.Fatalf("page: %d %q", res.StatusCode, got)
	}
	if ct := res.Header.Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("content-type %q", ct)
	}
	if csp := res.Header.Get("Content-Security-Policy"); csp != pageSandbox {
		t.Errorf("csp %q", csp)
	}
	if cd := res.Header.Get("Content-Disposition"); cd != "" {
		t.Errorf("content-disposition %q: a page is not an attachment", cd)
	}
	if rp := res.Header.Get("Referrer-Policy"); rp != "no-referrer" {
		t.Errorf("referrer-policy %q", rp)
	}

	// the name has to be the file's own; a made-up ticket is nothing
	tick := strings.TrimSuffix(strings.TrimPrefix(u, "/pages/"), "/A%20page.html")
	for _, bad := range []string{"/pages/" + tick + "/other.html", "/pages/nope/A%20page.html", "/v1/workspace/Artifacts/A%20page.html"} {
		res, err := http.Get(ts.URL + bad)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode == 200 {
			t.Errorf("GET %s served the page", bad)
		}
	}

	// an expired ticket is gone, and the next mint sweeps it
	srv.pageMu.Lock()
	pt := srv.pageTickets[tick]
	pt.exp = time.Now().Add(-time.Second)
	srv.pageTickets[tick] = pt
	srv.pageMu.Unlock()
	res, err = http.Get(ts.URL + u)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("expired ticket: %d", res.StatusCode)
	}
	if code, _ := mint("Artifacts/A page.html", "tok"); code != 200 {
		t.Fatalf("second mint: %d", code)
	}
	srv.pageMu.Lock()
	_, still := srv.pageTickets[tick]
	n := len(srv.pageTickets)
	srv.pageMu.Unlock()
	if still || n != 1 {
		t.Errorf("expired ticket not swept: still=%v n=%d", still, n)
	}
}

func jsonStr(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// TestPageTicketServesHandedOverPage: a page the desktop fetched from the
// web (a hub embed) is posted whole and served back under a ticket the same
// way — sandboxed, named by its file, capped, and the oldest handed-over
// text goes first when the tickets hold too much.
func TestPageTicketServesHandedOverPage(t *testing.T) {
	srv := New(&config.Config{APIToken: "tok"}, nil, nil, "", t.TempDir())
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	mint := func(body, token string) (int, map[string]any) {
		req, _ := http.NewRequest("POST", ts.URL+"/v1/pages", strings.NewReader(body))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var out map[string]any
		json.NewDecoder(res.Body).Decode(&out)
		return res.StatusCode, out
	}
	const page = "<!doctype html><title>Sheet</title><a href=\"#x\">x</a><h1 id=\"x\">表</h1>"
	if code, _ := mint(`{"name":"Sheet.html","html":`+jsonStr(page)+`}`, ""); code != http.StatusUnauthorized {
		t.Fatalf("mint without token: %d", code)
	}
	code, out := mint(`{"name":"../Brickbox Sheet.html","html":`+jsonStr(page)+`}`, "tok")
	if code != 200 {
		t.Fatalf("mint: %d %v", code, out)
	}
	u, _ := out["url"].(string)
	if !strings.HasPrefix(u, "/pages/") || !strings.HasSuffix(u, "/Brickbox%20Sheet.html") || out["size"] != float64(len(page)) {
		t.Fatalf("mint answer %v — the name keeps only its last element", out)
	}
	res, err := http.Get(ts.URL + u)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || string(got) != page {
		t.Fatalf("page: %d %q", res.StatusCode, got)
	}
	if csp := res.Header.Get("Content-Security-Policy"); csp != pageSandbox {
		t.Errorf("csp %q", csp)
	}
	if ct := res.Header.Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("content-type %q", ct)
	}
	if code, out := mint(`{"html":"<p>x"}`, "tok"); code != 200 || !strings.HasSuffix(out["url"].(string), "/page.html") {
		t.Errorf("nameless page: %d %v", code, out)
	}
	if code, _ := mint(`{"name":"big.html","html":`+jsonStr(strings.Repeat("a", webPageMax+1))+`}`, "tok"); code != http.StatusRequestEntityTooLarge {
		t.Errorf("page past the cap: %d", code)
	}

	// eight 8MB pages fill the 64MB the tickets may hold; a ninth pushes the oldest out
	big := `{"name":"big.html","html":` + jsonStr(strings.Repeat("b", webPageMax)) + `}`
	var first string
	for i := 0; i < 9; i++ {
		code, out := mint(big, "tok")
		if code != 200 {
			t.Fatalf("big mint %d: %d", i, code)
		}
		if i == 0 {
			first = strings.Split(out["url"].(string), "/")[2]
		}
		time.Sleep(2 * time.Millisecond) // distinct expiry times
	}
	srv.pageMu.Lock()
	held := 0
	for _, o := range srv.pageTickets {
		held += len(o.html)
	}
	_, still := srv.pageTickets[first]
	srv.pageMu.Unlock()
	if held > webPagesHeld || still {
		t.Errorf("held %d bytes (cap %d), oldest still there: %v", held, webPagesHeld, still)
	}
}
