package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"exe/internal/config"
)

// The UI files ship in the binary and change with every deploy: each one
// carries an ETag and no-cache, so a reload revalidates with a 304 instead
// of downloading the vendor scripts again; the service worker is served
// from the root, stamped with the build.
func TestUIFilesRevalidate(t *testing.T) {
	s := New(&config.Config{}, nil, nil, "", t.TempDir())
	mux := http.NewServeMux()
	mux.Handle("GET /ui/", uiStatic)
	mux.HandleFunc("GET /sw.js", handleServiceWorker)
	mux.HandleFunc("GET /", s.handleUI)
	get := func(path, ifNoneMatch string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", path, nil)
		if ifNoneMatch != "" {
			req.Header.Set("If-None-Match", ifNoneMatch)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	for _, p := range []string{"/", "/ui/manifest.json", "/ui/vendor/xterm.js", "/ui/offline.html", "/sw.js"} {
		rec := get(p, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: got %d", p, rec.Code)
		}
		tag := rec.Header().Get("ETag")
		if tag == "" || rec.Header().Get("Cache-Control") != "no-cache" {
			t.Errorf("%s: want an ETag and no-cache, got %q / %q", p, tag, rec.Header().Get("Cache-Control"))
		}
		if again := get(p, tag); again.Code != http.StatusNotModified {
			t.Errorf("%s: conditional GET got %d, want 304", p, again.Code)
		}
		if rec.Body.Len() == 0 {
			t.Errorf("%s: empty body", p)
		}
	}
	if ct := get("/", "").Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("index content type %q", ct)
	}
	sw := get("/sw.js", "")
	if ct := sw.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/javascript") {
		t.Errorf("worker content type %q", ct)
	}
	body := sw.Body.String()
	if strings.Contains(body, "__EXE_BUILD__") || !strings.Contains(body, `"`+uiBuild+`"`) {
		t.Errorf("worker is not stamped with the build %q", uiBuild)
	}
	if !strings.Contains(body, "/ui/offline.html") {
		t.Errorf("worker does not precache the offline page")
	}
	if len(uiBuild) != 12 {
		t.Errorf("build stamp %q", uiBuild)
	}
	// the desktop page carries the build it was served from, and every
	// response names the build the daemon ships, so an open page can tell
	// when it has gone stale
	if len(deskBuild) != 12 {
		t.Errorf("desk build stamp %q", deskBuild)
	}
	index := get("/", "").Body.String()
	if strings.Contains(index, "__EXE_BUILD__") || !strings.Contains(index, `const UI_BUILD = "`+deskBuild+`"`) {
		t.Errorf("index is not stamped with the build %q", deskBuild)
	}
	for _, p := range []string{"/", "/healthz", "/v1/ui/state"} {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
		if got := rec.Header().Get("X-Exe-Build"); got != deskBuild {
			t.Errorf("%s: X-Exe-Build %q, want %q", p, got, deskBuild)
		}
	}
	state := httptest.NewRecorder()
	s.Handler().ServeHTTP(state, httptest.NewRequest("GET", "/v1/ui/state", nil))
	s.ui.mu.Lock()
	ev := string(s.ui.eventLocked("", "en"))
	s.ui.mu.Unlock()
	if !strings.Contains(ev, `"build":"`+deskBuild+`"`) {
		t.Errorf("layout event does not name the build: %s", ev)
	}
	if rec := get("/ui/sw.js", ""); rec.Code != http.StatusNotFound {
		t.Errorf("unstamped worker reachable at /ui/sw.js: %d", rec.Code)
	}
	if rec := get("/somewhere", ""); rec.Code != http.StatusNotFound {
		t.Errorf("unknown path got %d", rec.Code)
	}
}

// The Services scan reads ss on Debian and busybox netstat on Alpine,
// which has no ss; each fixture is its tool's real shape.
// Configuration keeps a tab for the backend this node runs VMs with and
// drops the others' (a Mac has no Firecracker to set up), going by
// hostinfo's vm_backend. A group keyed on a name no platform reports would
// have its tab nowhere.
func TestConfigTabsFollowTheVMBackend(t *testing.T) {
	backends := map[string]string{"linux": "firecracker", "darwin": "virtualization", "windows": "qemu"}
	s := New(&config.Config{}, nil, nil, "", t.TempDir())
	rec := httptest.NewRecorder()
	s.handleHostInfo(rec, httptest.NewRequest("GET", "/v1/hostinfo", nil))
	var info struct {
		Backend string `json:"vm_backend"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if info.Backend != backends[runtime.GOOS] {
		t.Errorf("vm_backend on %s = %q, want %q", runtime.GOOS, info.Backend, backends[runtime.GOOS])
	}

	keyed := map[string]string{}
	for _, m := range regexp.MustCompile(`\{ group: "([^"]+)", backend: "([^"]+)"`).FindAllSubmatch(uiHTML, -1) {
		keyed[string(m[1])] = string(m[2])
	}
	if keyed["Firecracker"] != "firecracker" {
		t.Errorf("the Firecracker tab is keyed on %q, want the Linux backend", keyed["Firecracker"])
	}
	for group, backend := range keyed {
		known := false
		for _, b := range backends {
			known = known || b == backend
		}
		if !known {
			t.Errorf("the %s tab is keyed on %q, which no platform reports", group, backend)
		}
	}
}

func TestParsePorts(t *testing.T) {
	ss := `State  Recv-Q Send-Q Local Address:Port  Peer Address:Port Process
LISTEN 0      128        127.0.0.1:631        0.0.0.0:*
LISTEN 0      128          0.0.0.0:22         0.0.0.0:*     users:(("sshd",pid=600,fd=3))
LISTEN 0      511          0.0.0.0:8000       0.0.0.0:*     users:(("python3",pid=900,fd=5))
LISTEN 0      4096   127.0.0.53%lo:53         0.0.0.0:*     users:(("systemd-resolve",pid=300,fd=14))
LISTEN 0      128            [::1]:631           [::]:*
`
	got := parsePorts(ss)
	if len(got) != 1 || got[0].Port != 8000 || got[0].Process != "python3" {
		t.Fatalf("ss parse = %+v; want one 8000/python3", got)
	}

	netstat := `Active Internet connections (only servers)
Proto Recv-Q Send-Q Local Address           Foreign Address         State       PID/Program name
tcp        0      0 0.0.0.0:22              0.0.0.0:*               LISTEN      614/sshd
tcp        0      0 0.0.0.0:8080            0.0.0.0:*               LISTEN      1041/httpd
tcp        0      0 127.0.0.1:6379          0.0.0.0:*               LISTEN      1100/redis-server
tcp        0      0 ::1:9090                :::*                    LISTEN      1200/node
tcp        0      0 :::22                   :::*                    LISTEN      614/sshd
`
	got = parsePorts(netstat)
	if len(got) != 1 || got[0].Port != 8080 || got[0].Process != "httpd" {
		t.Fatalf("netstat parse = %+v; want one 8080/httpd", got)
	}

	// Unprivileged netstat has no process column; the port still lists.
	plain := "tcp        0      0 0.0.0.0:3000            0.0.0.0:*               LISTEN\n"
	got = parsePorts(plain)
	if len(got) != 1 || got[0].Port != 3000 || got[0].Process != "" {
		t.Fatalf("plain netstat parse = %+v; want one bare 3000", got)
	}
}

func TestParseGuestStat(t *testing.T) {
	st := parseGuestStat("load 0.13 0.09 0.04\nos alpine 3.24.2\n")
	if st.OS != "alpine" || st.Version != "3.24.2" ||
		len(st.Load) != 3 || st.Load[0] != "0.13" || st.Load[2] != "0.04" {
		t.Fatalf("parseGuestStat = %+v", st)
	}
	// A guest without os-release still answers its load.
	st = parseGuestStat("load 1.00 0.50 0.25\n")
	if st.OS != "" || len(st.Load) != 3 {
		t.Fatalf("load-only parse = %+v", st)
	}
	if st = parseGuestStat("load \n"); st.Load != nil {
		t.Fatalf("empty loadavg should stay nil, got %+v", st)
	}
}
