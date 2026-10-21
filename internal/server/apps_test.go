package server

import (
	"encoding/json"
	"exe/internal/config"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestSystemAppIconComesFromEmbeddedBundle(t *testing.T) {
	m, err := loadSysAppMeta("Mac OS 9")
	if err != nil {
		t.Fatal(err)
	}
	svg, err := sysAppsFS.ReadFile("sysapps/macos9/icon.svg")
	if err != nil {
		t.Fatal(err)
	}
	if m.SystemIcon != string(svg) || !strings.Contains(m.SystemIcon, `viewBox="0 0 32 32"`) {
		t.Fatal("missing embedded Mac OS 9 icon")
	}
}

func TestDiskAppCannotSupplyInlineSystemIcon(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Mac OS 9")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{
		"app.json":   `{"title":"Replacement","icon":"icon.svg","system_icon":"<svg onload=evil()>"}`,
		"index.html": "<title>Replacement</title>",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	m, err := (&Server{}).loadAppMeta(root, "Mac OS 9")
	if err != nil {
		t.Fatal(err)
	}
	if m.SystemIcon != "" || m.Icon != "icon.svg" {
		t.Fatalf("disk bundle must remain an image resource: %+v", m)
	}
}

// These aliases shipped as app IDs, so bookmarks, disk overrides and data
// requests from an older desktop must still resolve to the same app.
func TestSystemAppAliases(t *testing.T) {
	s := New(&config.Config{APIToken: "test-secret"}, nil, nil, "", t.TempDir())
	handler := s.Handler()
	for _, pair := range [][2]string{{"Mac OS 9", "macos9"}, {"Hub", "hub"}, {"BluePencil", "bluepencil"}} {
		t.Run(pair[1], func(t *testing.T) {
			for _, name := range pair {
				m, err := loadSysAppMeta(name)
				if err != nil || m.Name != pair[1] {
					t.Fatalf("metadata for %q: %+v, %v", name, m, err)
				}
				for _, asset := range []string{"", "icon.svg"} {
					w := appRequest(handler, "GET", "/apps/"+url.PathEscape(name)+"/"+asset, "", 0)
					if w.Code != 200 || w.Body.Len() == 0 {
						t.Fatalf("%s/%s: %d", name, asset, w.Code)
					}
					canonical := appRequest(handler, "GET", "/apps/"+pair[1]+"/"+asset, "", 0)
					if w.Body.String() != canonical.Body.String() {
						t.Fatalf("alias serves different asset: %s/%s", name, asset)
					}
				}
			}
		})
	}
	w := appRequest(handler, "GET", "/apps/Mac%20OS%209/core/rfb.js", "", 0)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "class RFB") {
		t.Fatal("legacy nested module failed")
	}
	var apps []appMeta
	w = appRequest(handler, "GET", "/v1/apps", "", 0)
	if err := json.Unmarshal(w.Body.Bytes(), &apps); err != nil {
		t.Fatal(err)
	}
	// one entry a bundle: no alias lists a system app twice
	bundles, _ := fs.ReadDir(sysAppsFS, "sysapps")
	if len(apps) != len(bundles) || len(apps) < 4 {
		t.Fatalf("unexpected app list (%d bundles): %+v", len(bundles), apps)
	}
	for _, app := range apps {
		if app.Name != canonicalAppName(app.Name) || app.SystemIcon == "" {
			t.Fatalf("bad system app: %+v", app)
		}
	}
}

func appRequest(handler http.Handler, method, path, body string, seq int) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://exe.test"+path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer test-secret")
	if seq > 0 {
		r.Header.Set("X-Exe-Seq", strconv.Itoa(seq))
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func writeTestApp(t *testing.T, root, name, title string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	meta, _ := json.Marshal(map[string]string{"title": title})
	for name, data := range map[string][]byte{"app.json": meta, "index.html": []byte(title)} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAppAliasDiskPrecedence(t *testing.T) {
	state, extra := t.TempDir(), t.TempDir()
	s := New(&config.Config{APIToken: "test-secret", AppsDirs: []string{extra}}, nil, nil, "", state)
	h := s.Handler()
	writeTestApp(t, extra, "hub", "Extra canonical")
	writeTestApp(t, s.appsDir(), "Hub", "Primary legacy")
	check := func(title string) {
		t.Helper()
		for _, name := range []string{"Hub", "hub"} {
			w := appRequest(h, "GET", "/apps/"+name+"/", "", 0)
			if w.Code != 200 || w.Body.String() != title {
				t.Fatalf("%s resolved to %d %q, want %q", name, w.Code, w.Body.String(), title)
			}
		}
		var apps []appMeta
		w := appRequest(h, "GET", "/v1/apps", "", 0)
		if err := json.Unmarshal(w.Body.Bytes(), &apps); err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, app := range apps {
			if canonicalAppName(app.Name) == "hub" {
				n++
				if app.Name != "hub" || app.Title != title || app.SystemIcon != "" {
					t.Fatalf("wrong metadata: %+v", app)
				}
			}
		}
		if n != 1 {
			t.Fatalf("listed Hub %d times", n)
		}
	}
	check("Primary legacy") // root priority wins over canonical spelling
	writeTestApp(t, s.appsDir(), "hub", "Primary canonical")
	check("Primary canonical") // canonical wins within the same root
	if err := os.Remove(filepath.Join(s.appsDir(), "hub", "app.json")); err != nil {
		t.Fatal(err)
	}
	check("Primary legacy") // a partial bundle cannot hide a valid app
	writeTestApp(t, s.appsDir(), "World Clock", "World Clock")
	w := appRequest(h, "GET", "/apps/World%20Clock/", "", 0)
	if w.Code != 200 || w.Body.String() != "World Clock" {
		t.Fatal("user app names must keep working")
	}
}

func TestAppAliasSharesDataVersionsAndEvents(t *testing.T) {
	for _, pair := range [][2]string{{"Mac OS 9", "macos9"}, {"Hub", "hub"}, {"BluePencil", "bluepencil"}} {
		t.Run(pair[1], func(t *testing.T) {
			s := New(&config.Config{APIToken: "test-secret"}, nil, nil, "", t.TempDir())
			h := s.Handler()
			events := make(chan []byte, 4)
			s.appEv.subs = map[chan []byte]struct{}{events: {}}
			legacy := "/v1/apps/" + url.PathEscape(pair[0]) + "/data/config.json"
			canonical := "/v1/apps/" + pair[1] + "/data/config.json"
			for _, put := range []struct {
				path, body string
				seq        int
			}{{legacy, `{"value":1}`, 10}, {canonical, `{"value":2}`, 20}, {legacy, `{"value":0}`, 15}} {
				w := appRequest(h, "PUT", put.path, put.body, put.seq)
				if w.Code != 200 {
					t.Fatalf("PUT: %d %s", w.Code, w.Body.String())
				}
			}
			for _, path := range []string{legacy, canonical} {
				w := appRequest(h, "GET", path, "", 0)
				if w.Code != 200 || w.Body.String() != `{"value":2}` {
					t.Fatalf("stale write clobbered shared data: %d %s", w.Code, w.Body.String())
				}
			}
			if s.appSeq[pair[0]+"/config.json"].seq != 20 || len(s.appSeq) != 1 {
				t.Fatalf("split version namespace: %v", s.appSeq)
			}
			if _, err := os.Stat(filepath.Join(s.StateDir, "appdata", pair[0], "config.json")); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(s.StateDir, "appdata", pair[1])); !os.IsNotExist(err) {
				t.Fatal("created a second data namespace")
			}
			if w := appRequest(h, "DELETE", canonical, "", 0); w.Code != 200 {
				t.Fatalf("DELETE: %d", w.Code)
			}
			if w := appRequest(h, "GET", legacy, "", 0); w.Code != 404 {
				t.Fatalf("alias still sees deleted data: %d", w.Code)
			}
			if len(events) != 3 {
				t.Fatalf("expected two saves and one delete, got %d events", len(events))
			}
			for i := 0; i < 3; i++ {
				var ev struct {
					App     string
					Deleted bool
				}
				if err := json.Unmarshal(<-events, &ev); err != nil {
					t.Fatal(err)
				}
				if ev.App != pair[0] || ev.Deleted != (i == 2) {
					t.Fatalf("legacy event namespace changed: %+v", ev)
				}
			}
		})
	}
}

// A movie in the Workspace streams: the file GET answers a Range request
// with 206 and only the requested bytes (Safari refuses media served
// whole), and a movie extension gets its media type even where the host
// has no mime table.
func TestWorkspaceFileGetServesRanges(t *testing.T) {
	s := New(&config.Config{APIToken: "test-secret"}, nil, nil, "", t.TempDir())
	h := s.Handler()
	if err := os.MkdirAll(s.workspaceDir(), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.workspaceDir(), "clip.mp4"), []byte("0123456789"), 0600); err != nil {
		t.Fatal(err)
	}
	w := appRequest(h, "GET", "/v1/workspace/clip.mp4", "", 0)
	if w.Code != 200 || w.Body.String() != "0123456789" {
		t.Fatalf("whole file: %d %q", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "video/mp4" {
		t.Fatalf("Content-Type: %q", got)
	}
	if got := w.Header().Get("Accept-Ranges"); got != "bytes" {
		t.Fatalf("Accept-Ranges: %q", got)
	}
	r := httptest.NewRequest("GET", "http://exe.test/v1/workspace/clip.mp4", nil)
	r.Header.Set("Authorization", "Bearer test-secret")
	r.Header.Set("Range", "bytes=2-5")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != 206 || rec.Body.String() != "2345" {
		t.Fatalf("range: %d %q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Range"); got != "bytes 2-5/10" {
		t.Fatalf("Content-Range: %q", got)
	}
}

// zeros reads as an endless run of zero bytes.
type zeros struct{}

func (zeros) Read(b []byte) (int, error) { clear(b); return len(b), nil }

// A PUT stores up to 100 MB, streamed to disk as it arrives, and refuses
// one byte more — by its Content-Length when it says one, else by the
// count — leaving no file or temp file behind.
func TestWorkspacePutTakes100MB(t *testing.T) {
	s := New(&config.Config{APIToken: "test-secret"}, nil, nil, "", t.TempDir())
	h := s.Handler()
	put := func(body io.Reader, length int64) *httptest.ResponseRecorder {
		r := httptest.NewRequest("PUT", "http://exe.test/v1/workspace/big.bin", body)
		r.Header.Set("Authorization", "Bearer test-secret")
		r.ContentLength = length
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	p := filepath.Join(s.workspaceDir(), "big.bin")
	if w := put(io.LimitReader(zeros{}, fileMax), -1); w.Code != 200 {
		t.Fatalf("PUT of 100 MB: %d %s", w.Code, w.Body.String())
	}
	if fi, err := os.Stat(p); err != nil || fi.Size() != fileMax {
		t.Fatalf("stored: %v %v", fi, err)
	}
	os.Remove(p)
	if w := put(io.LimitReader(zeros{}, fileMax+1), -1); w.Code != 413 {
		t.Fatalf("PUT of 100 MB and a byte, unannounced: %d %s", w.Code, w.Body.String())
	}
	if w := put(strings.NewReader("x"), fileMax+1); w.Code != 413 {
		t.Fatalf("PUT announcing 100 MB and a byte: %d %s", w.Code, w.Body.String())
	}
	left, _ := os.ReadDir(s.workspaceDir())
	if len(left) != 0 {
		t.Fatalf("a refused PUT left %v", left)
	}
}

// An app says in its app.json what it keeps for when exe does not answer;
// the list of apps passes on only the addresses that are the app's to keep.
func TestAppOfflineAddresses(t *testing.T) {
	apps := t.TempDir()
	write := func(name, manifest string) {
		t.Helper()
		dir := filepath.Join(apps, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for file, body := range map[string]string{"app.json": manifest, "index.html": "<!doctype html>"} {
			if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	write("plain", `{"title": "Plain"}`)
	write("off", `{"title": "Off", "offline": false}`)
	write("folder", `{"title": "Folder", "offline": true}`)
	write("tracker", `{"title": "Tracker", "offline": {
		"keep": ["/platinum/popup.css", "/v1/svc/tracker/v1/snapshot", "/v1/svc/tracker/v1/list?state=all",
			"/v1/apps/tracker/data/notes.json", "/apps/tracker/extra.js",
			"/v1/vms", "/v1/apps/Notes/data/notes.json", "/v1/svc/tracker/../../vms", "https://example.com/v1/svc/x",
			"//example.com/v1/svc/x", "/v1/svc/tracker/v1/snapshot?token=abc", "/v1/svc/tracker/v1/a#b", "/v1/svc/", "v1/svc/x"],
		"fixed": ["/v1/svc/tracker/v1/files/", "/v1/svc/tracker/v1/files", "/v1/svc/tracker/v1/files/?x=1", "/v1/", "/"],
		"wanted": "/v1/svc/tracker/v1/wanted"}}`)
	write("greedy", `{"title": "Greedy", "offline": {"keep": ["/v1/hostinfo"], "wanted": "/v1/workspace"}}`)

	s := &Server{StateDir: t.TempDir()}
	s.cfg.Store(&config.Config{AppsDirs: []string{apps}})
	got := map[string]*appOffline{}
	for _, a := range s.listApps() {
		got[a.Name] = a.Offline
	}
	if got["plain"] != nil || got["off"] != nil {
		t.Fatalf("an app that did not ask keeps something: %+v, %+v", got["plain"], got["off"])
	}
	if o := got["folder"]; o == nil || len(o.Keep) != 0 || len(o.Fixed) != 0 {
		t.Fatalf(`"offline": true is %+v`, o)
	}
	o := got["tracker"]
	if o == nil {
		t.Fatal("the tracker keeps nothing")
	}
	wantKeep := []string{"/platinum/popup.css", "/v1/svc/tracker/v1/snapshot", "/v1/svc/tracker/v1/list?state=all",
		"/v1/apps/tracker/data/notes.json", "/apps/tracker/extra.js"}
	if !reflect.DeepEqual(o.Keep, wantKeep) {
		t.Errorf("keeps %q, want %q", o.Keep, wantKeep)
	}
	if want := []string{"/v1/svc/tracker/v1/files/"}; !reflect.DeepEqual(o.Fixed, want) {
		t.Errorf("fixed %q, want %q", o.Fixed, want)
	}
	if o.Wanted != "/v1/svc/tracker/v1/wanted" {
		t.Errorf("wanted %q", o.Wanted)
	}
	// an app that asked only for what is not its own keeps its folder and no more
	if g := got["greedy"]; g == nil || len(g.Keep) != 0 || g.Wanted != "" {
		t.Errorf("an app that asked for the desk's own: %+v", g)
	}
	// and the desk is told, in the words the worker reads
	b, _ := json.Marshal(o)
	if !strings.Contains(string(b), `"keep":["/platinum/popup.css"`) || !strings.Contains(string(b), `"fixed":["/v1/svc/tracker/v1/files/"]`) {
		t.Errorf("as JSON: %s", b)
	}
	if b, _ := json.Marshal(got["folder"]); string(b) != "{}" {
		t.Errorf(`"offline": true as JSON: %s`, b)
	}
}
