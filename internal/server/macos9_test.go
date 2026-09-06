package server

import (
	"context"
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
		{"POST", "/v1/macos9/start", "test-secret", "https://unrelated.example", 403},
		{"POST", "/v1/macos9/cancel", "test-secret", "https://unrelated.example", 403},
		{"POST", "/v1/macos9/finish", "test-secret", "", 409},
		{"GET", "/v1/macos9/console", "test-secret", "", 503},
		{"GET", "/v1/macos9/console", "test-secret", "https://unrelated.example", 403},
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
func TestMacOS9ConsoleBinaryProxy(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "mac-os9")
	os.MkdirAll(dir, 0700)
	ln, err := net.Listen("unix", filepath.Join(dir, "vnc.sock"))
	if err != nil {
		t.Skip(err)
	}
	defer ln.Close()
	go func() {
		c, e := ln.Accept()
		if e != nil {
			return
		}
		defer c.Close()
		io.Copy(c, c)
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
	payload := []byte{0, 1, 2, 255, 128, 0, 10}
	if err = ws.Write(ctx, websocket.MessageBinary, payload); err != nil {
		t.Fatal(err)
	}
	typ, data, err := ws.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if typ != websocket.MessageBinary || string(data) != string(payload) {
		t.Fatalf("corrupt VNC transport: %v %v", typ, data)
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
