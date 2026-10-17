package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func davTestServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	s := &Server{StateDir: t.TempDir()}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/dav", s.handleShare)
	mux.HandleFunc("POST /v1/dav", s.handleShare)
	mux.Handle("/Workspace/", s.davHandler())
	mux.Handle("/Workspace", s.davHandler())
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return s, ts
}

func davShare(t *testing.T, ts *httptest.Server, method string) shareInfo {
	t.Helper()
	req, _ := http.NewRequest(method, ts.URL+"/v1/dav", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var info shareInfo
	if err := json.NewDecoder(res.Body).Decode(&info); err != nil {
		t.Fatal(err)
	}
	return info
}

func davDo(t *testing.T, ts *httptest.Server, method, p, password, body string, headers map[string]string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(method, ts.URL+p, strings.NewReader(body))
	if password != "" {
		req.SetBasicAuth("anyone", password)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Body.Close() })
	return res
}

func TestDAVNeedsTheSharePassword(t *testing.T) {
	_, ts := davTestServer(t)
	info := davShare(t, ts, http.MethodGet)
	if len(info.Password) != 12 || info.User != "workspace" || !strings.HasSuffix(info.Mac, ":"+strings.TrimPrefix(ts.URL[strings.LastIndex(ts.URL, ":"):], ":")+"/Workspace/") && !strings.Contains(info.Mac, "10.0.2.2") {
		t.Fatalf("share info: %+v", info)
	}
	if res := davDo(t, ts, http.MethodPut, "/Workspace/hello.txt", "", "hi", nil); res.StatusCode != http.StatusUnauthorized || !strings.HasPrefix(res.Header.Get("WWW-Authenticate"), "Basic") {
		t.Fatalf("no password: %d %q", res.StatusCode, res.Header.Get("WWW-Authenticate"))
	}
	if res := davDo(t, ts, http.MethodPut, "/Workspace/hello.txt", "wrong"+info.Password, "hi", nil); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong password: %d", res.StatusCode)
	}
	if res := davDo(t, ts, http.MethodPut, "/Workspace/hello.txt", info.Password, "hi", nil); res.StatusCode != http.StatusCreated {
		t.Fatalf("put: %d", res.StatusCode)
	}
	if again := davShare(t, ts, http.MethodGet); again.Password != info.Password {
		t.Fatal("the password changed on a plain read")
	}
	renewed := davShare(t, ts, http.MethodPost)
	if renewed.Password == info.Password {
		t.Fatal("POST did not renew the password")
	}
	if res := davDo(t, ts, http.MethodPut, "/Workspace/hello2.txt", info.Password, "hi", nil); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old password after renew: %d", res.StatusCode)
	}
}

func TestDAVWritesAndListsTheWorkspace(t *testing.T) {
	s, ts := davTestServer(t)
	pw := davShare(t, ts, http.MethodGet).Password
	if res := davDo(t, ts, "MKCOL", "/Workspace/Saves", pw, "", nil); res.StatusCode != http.StatusCreated {
		t.Fatalf("mkcol: %d", res.StatusCode)
	}
	if res := davDo(t, ts, http.MethodPut, "/Workspace/Saves/game.sav", pw, "campaign", nil); res.StatusCode != http.StatusCreated {
		t.Fatalf("put: %d", res.StatusCode)
	}
	b, err := os.ReadFile(filepath.Join(s.workspaceDir(), "Saves", "game.sav"))
	if err != nil || string(b) != "campaign" {
		t.Fatalf("on disk: %q %v", b, err)
	}
	res := davDo(t, ts, "PROPFIND", "/Workspace/Saves/", pw, "", map[string]string{"Depth": "1"})
	if res.StatusCode != http.StatusMultiStatus {
		t.Fatalf("propfind: %d", res.StatusCode)
	}
	var sb strings.Builder
	buf := make([]byte, 1<<16)
	for {
		n, err := res.Body.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			break
		}
	}
	if !strings.Contains(sb.String(), "game.sav") {
		t.Fatalf("listing lacks the file: %s", sb.String())
	}
	if res := davDo(t, ts, "MOVE", "/Workspace/Saves/game.sav", pw, "", map[string]string{"Destination": ts.URL + "/Workspace/Saves/won.sav"}); res.StatusCode != http.StatusCreated && res.StatusCode != http.StatusNoContent {
		t.Fatalf("move: %d", res.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(s.workspaceDir(), "Saves", "won.sav")); err != nil {
		t.Fatal("the move did not land:", err)
	}
	if res := davDo(t, ts, http.MethodDelete, "/Workspace/Saves/won.sav", pw, "", nil); res.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: %d", res.StatusCode)
	}
	if res := davDo(t, ts, http.MethodDelete, "/Workspace/", pw, "", nil); res.StatusCode < 400 {
		t.Fatalf("deleting the Workspace itself answered %d", res.StatusCode)
	}
	// The Mac's sidecar files go through, and are nobody's news.
	if res := davDo(t, ts, http.MethodPut, "/Workspace/Saves/._won.sav", pw, "rsrc", nil); res.StatusCode != http.StatusCreated {
		t.Fatalf("dot file: %d", res.StatusCode)
	}
}

func TestDAVStaysInTheWorkspace(t *testing.T) {
	s, ts := davTestServer(t)
	pw := davShare(t, ts, http.MethodGet).Password
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("no"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(s.workspaceDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(s.workspaceDir(), "out")); err != nil {
		t.Fatal(err)
	}
	if res := davDo(t, ts, http.MethodGet, "/Workspace/out/secret", pw, "", nil); res.StatusCode == http.StatusOK {
		t.Fatal("a symlink out of the folder was followed")
	}
	if res := davDo(t, ts, http.MethodPut, "/Workspace/out/planted", pw, "x", nil); res.StatusCode < 400 {
		t.Fatalf("a write through the symlink answered %d", res.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(outside, "planted")); err == nil {
		t.Fatal("the write landed outside")
	}
	if res := davDo(t, ts, http.MethodGet, "/Workspace/../workspace.password", pw, "", nil); res.StatusCode == http.StatusOK {
		t.Fatal("dot-dot left the folder")
	}
}
