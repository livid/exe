package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"exe/internal/config"
	"github.com/coder/websocket"
)

func TestMacOS9APIAuthAndOrigin(t *testing.T) {
	s := New(&config.Config{APIToken: "test-secret"}, nil, nil, "", t.TempDir())
	for _, tt := range []struct {
		method, path, token, origin string
		code                        int
	}{
		{"GET", "/v1/macos9", "", "", 401},
		{"GET", "/v1/macos9/console", "", "", 401},
		{"POST", "/v1/macos9/start", "", "", 401},
		{"GET", "/v1/macos9", "test-secret", "", 200},
		{"GET", "/v1/macos9/cd", "", "", 401},
		{"POST", "/v1/macos9/cd", "", "", 401},
		{"POST", "/v1/macos9/cd/upload", "", "", 401},
		{"GET", "/v1/macos9/cd", "test-secret", "", 200},
		{"POST", "/v1/macos9/cd", "test-secret", "https://unrelated.example", 403},
		{"POST", "/v1/macos9/cd/upload", "test-secret", "https://unrelated.example", 403},
		{"POST", "/v1/macos9/start", "test-secret", "https://unrelated.example", 403},
		{"POST", "/v1/macos9/cancel", "test-secret", "https://unrelated.example", 403},
		{"POST", "/v1/macos9/finish", "test-secret", "", 409},
		{"GET", "/v1/macos9/console", "test-secret", "", 503},
		{"GET", "/v1/macos9/console", "test-secret", "https://unrelated.example", 403},
		// The Mac OS X app's Mac answers the same API under its own name.
		{"GET", "/v1/macosx", "", "", 401},
		{"GET", "/v1/macosx", "test-secret", "", 200},
		{"GET", "/v1/macosx/cd", "test-secret", "", 200},
		{"POST", "/v1/macosx/start", "test-secret", "https://unrelated.example", 403},
		{"POST", "/v1/macosx/cd/upload", "test-secret", "https://unrelated.example", 403},
		{"POST", "/v1/macosx/finish", "test-secret", "", 409},
		{"POST", "/v1/macosx/pointer", "", "", 401},
		{"POST", "/v1/macosx/pointer", "test-secret", "https://unrelated.example", 403},
		{"POST", "/v1/macosx/pointer", "test-secret", "", 400},
		{"GET", "/v1/macosx/console", "test-secret", "", 503},
	} {
		r := httptest.NewRequest(tt.method, "http://exe.test"+tt.path, nil)
		if tt.token != "" {
			r.Header.Set("Authorization", "Bearer "+tt.token)
		}
		if tt.origin != "" {
			r.Header.Set("Origin", tt.origin)
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != tt.code {
			t.Errorf("%s %s: %d %s", tt.method, tt.path, w.Code, w.Body.String())
		}
	}
}

// The console carries the Mac's VNC session to the browser unchanged, through the relay that
// paces it (internal/vncpace): a handshake, a request, an update.
func TestMacOS9ConsoleBinaryProxy(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "mac-os9")
	os.MkdirAll(dir, 0700)
	ln, err := net.Listen("unix", filepath.Join(dir, "vnc.sock"))
	if err != nil {
		t.Skip(err)
	}
	defer ln.Close()
	update := []byte{0, 0, 0, 1, 0, 0, 0, 0, 0, 1, 0, 1, 0, 0, 0, 0, 1, 2, 3, 4} // one raw pixel
	requests := make(chan []byte, 2)
	go func() {
		c, e := ln.Accept()
		if e != nil {
			return
		}
		defer c.Close()
		c.Write([]byte("RFB 003.008\n"))
		io.ReadFull(c, make([]byte, 12))
		c.Write([]byte{1, 1})
		io.ReadFull(c, make([]byte, 1))
		c.Write([]byte{0, 0, 0, 0})
		io.ReadFull(c, make([]byte, 1))
		c.Write([]byte{0, 1, 0, 1, 32, 24, 0, 1, 0, 255, 0, 255, 0, 255, 16, 8, 0, 0, 0, 0, 0, 0, 0, 0})
		req := make([]byte, 10)
		if _, e := io.ReadFull(c, req); e == nil {
			requests <- req
			c.Write(update)
		}
		io.Copy(io.Discard, c)
	}()
	srv := httptest.NewServer(New(&config.Config{APIToken: "test-secret"}, nil, nil, "", root).Handler())
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/v1/macos9/console?token=test-secret", &websocket.DialOptions{Subprotocols: []string{"binary"}})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	var got []byte
	read := func(n int) []byte {
		for len(got) < n {
			typ, data, err := ws.Read(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if typ != websocket.MessageBinary {
				t.Fatalf("message type %v", typ)
			}
			got = append(got, data...)
		}
		b := got[:n]
		got = got[n:]
		return b
	}
	send := func(b []byte) {
		if err := ws.Write(ctx, websocket.MessageBinary, b); err != nil {
			t.Fatal(err)
		}
	}
	if v := read(12); string(v) != "RFB 003.008\n" {
		t.Fatalf("corrupt VNC transport: %q", v)
	}
	send([]byte("RFB 003.008\n"))
	read(2)
	send([]byte{1})
	read(4)
	send([]byte{1})
	read(24)
	send([]byte{3, 0, 0, 0, 0, 0, 0, 1, 0, 1})
	if r := <-requests; r[0] != 3 || r[1] != 0 {
		t.Fatalf("the request reached the Mac as %v", r)
	}
	if u := read(len(update)); string(u) != string(update) {
		t.Fatalf("corrupt VNC transport: %v", u)
	}
}
func TestMacOS9BuiltInAssets(t *testing.T) {
	if !sysAppExists("macos9") {
		t.Fatal("missing built-in app")
	}
	meta, err := loadSysAppMeta("macos9")
	if err != nil {
		t.Fatal(err)
	}
	if meta.Title != "Mac OS 9" || meta.Window == nil || meta.Window.Grow {
		t.Fatalf("bad app metadata: %+v", meta)
	}
	for _, p := range []string{"sysapps/macos9/app.js", "sysapps/macos9/core/rfb.js", "sysapps/macos9/vendor/pako/lib/zlib/inflate.js"} {
		if _, err := sysAppsFS.ReadFile(p); err != nil {
			t.Error(err)
		}
	}
}

// The Mac OS X app is a page of its own around the Mac OS 9 app's viewer:
// what it links must be in the binary, and its Mac must be another Mac.
func TestMacOSXBuiltInAppAndItsOwnMac(t *testing.T) {
	meta, err := loadSysAppMeta("macosx")
	if err != nil {
		t.Fatal(err)
	}
	if meta.Title != "Mac OS X" || meta.Window == nil || meta.Window.Grow || !strings.Contains(meta.SystemIcon, `viewBox="0 0 32 32"`) {
		t.Fatalf("bad app metadata: %+v", meta)
	}
	page, err := sysAppsFS.ReadFile("sysapps/macosx/index.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`data-api="/v1/macosx"`, `href="../macos9/mac.css"`, `src="../macos9/app.js"`} {
		if !strings.Contains(string(page), want) {
			t.Errorf("page lacks %s", want)
		}
	}
	root := t.TempDir()
	s := New(&config.Config{}, nil, nil, "", root)
	h := s.Handler()
	for _, p := range []string{"/apps/macosx/", "/apps/macos9/mac.css", "/apps/macos9/app.js", "/apps/macosx/icon.svg"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "http://exe.test"+p, nil))
		if w.Code != 200 {
			t.Errorf("%s: %d", p, w.Code)
		}
	}
	// An installed Mac OS 9 says nothing about Mac OS X: each has its root.
	if err := os.MkdirAll(filepath.Join(root, "mac-os9"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "mac-os9", "installed.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	status := func(path string) (st struct {
		Installed bool
		Steps     []struct{ Title string }
	}) {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "http://exe.test"+path, nil))
		if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil {
			t.Fatal(err)
		}
		return st
	}
	if nine, ten := status("/v1/macos9"), status("/v1/macosx"); !nine.Installed || ten.Installed || ten.Steps[5].Title != "Install Mac OS X" || nine.Steps[5].Title != "Install Mac OS 9" {
		t.Fatalf("the two Macs are not apart: %+v %+v", nine, ten)
	}
}

// A viewer tells the daemon which correction the Mac's pointer needs, and
// the status hands it to every viewer; only Mac OS X's Mac takes one.
func TestMacPointerTrimAPI(t *testing.T) {
	s := New(&config.Config{}, nil, nil, "", t.TempDir())
	h := s.Handler()
	post := func(path, body string) (int, string) {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("POST", "http://exe.test"+path, strings.NewReader(body)))
		return w.Code, w.Body.String()
	}
	if code, body := post("/v1/macosx/pointer", `{"trim":0.15}`); code != 200 || !strings.Contains(body, `"trim":0.15`) {
		t.Fatalf("%d %s", code, body)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "http://exe.test/v1/macosx", nil))
	if !strings.Contains(w.Body.String(), `"trim":0.15`) {
		t.Fatalf("the status lacks the trim: %s", w.Body.String())
	}
	if code, _ := post("/v1/macosx/pointer", `{"trim":0.3}`); code != 409 {
		t.Fatalf("an unknown correction: %d", code)
	}
	if code, _ := post("/v1/macos9/pointer", `{"trim":0.15}`); code != 409 {
		t.Fatalf("Mac OS 9 took a correction: %d", code)
	}
}

func TestMacOS9StartFailureRemainsInStatus(t *testing.T) {
	for _, installed := range []bool{false, true} {
		t.Run(fmt.Sprintf("installed=%v", installed), func(t *testing.T) {
			root := t.TempDir()
			macDir := filepath.Join(root, "mac-os9")
			// Deterministically fail saving progress before any setup worker starts.
			if err := os.MkdirAll(filepath.Join(macDir, "setup.json.tmp"), 0700); err != nil {
				t.Fatal(err)
			}
			if installed {
				if err := os.WriteFile(filepath.Join(macDir, "installed.json"), []byte(`{}`), 0600); err != nil {
					t.Fatal(err)
				}
			}
			s := New(&config.Config{APIToken: "test-secret"}, nil, nil, "", root)
			handler := s.Handler()
			start := appRequest(handler, "POST", "/v1/macos9/start", "", 0)
			if start.Code != 500 || !strings.Contains(start.Body.String(), "Cannot save setup progress:") {
				t.Fatalf("startup failure: %d %s", start.Code, start.Body.String())
			}
			// Polling must not replace the failed action with a generic stopped or
			// preparing message, including when an installed guest is shut down.
			for i := 0; i < 2; i++ {
				w := appRequest(handler, "GET", "/v1/macos9", "", 0)
				var status struct {
					Phase, Message             string
					Active, Running, Installed bool
					Steps                      []struct{ State, Detail string }
				}
				if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
					t.Fatal(err)
				}
				if w.Code != 200 || status.Phase != "error" || status.Active || status.Running || status.Installed != installed || status.Message != strings.TrimSpace(start.Body.String()) {
					t.Fatalf("lost startup failure: %+v", status)
				}
				if len(status.Steps) != 6 || status.Steps[0].State != "error" || status.Steps[0].Detail != status.Message {
					t.Fatalf("lost failed step: %+v", status.Steps)
				}
			}
		})
	}
}

func TestMacOS9CDUploadAPI(t *testing.T) {
	root := t.TempDir()
	s := New(&config.Config{APIToken: "test-secret"}, nil, nil, "", root)
	for i := 0; i < 2; i++ {
		r := httptest.NewRequest("POST", "http://exe.test/v1/macos9/cd/upload?filename=Game.iso", strings.NewReader("test disc"))
		r.Header.Set("Authorization", "Bearer test-secret")
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		var result struct{ Filename string }
		if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Filename == "" {
			t.Fatalf("upload: %d %s", w.Code, w.Body.String())
		}
	}
	r := httptest.NewRequest("POST", "http://exe.test/v1/macos9/cd/upload?filename=..%2Fescape.iso", strings.NewReader("bad"))
	r.Header.Set("Authorization", "Bearer test-secret")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatalf("accepted traversal: %d", w.Code)
	}
	files, _ := os.ReadDir(filepath.Join(root, "mac-os9", "media"))
	if len(files) != 2 {
		t.Fatalf("saved incorrect images: %v", files)
	}
}
