package server

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

// The media proxy signs the body's digest with the node key and streams
// the bytes on; the hub's job comes back as it was, and nothing stays
// staged.
func TestHubMediaSignsAndStreams(t *testing.T) {
	body := bytes.Repeat([]byte("a video, more or less "), 50000)
	var gotLen int
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/media" {
			http.NotFound(w, r)
			return
		}
		b, _ := io.ReadAll(r.Body)
		gotLen = len(b)
		pub, _ := base64.StdEncoding.DecodeString(r.Header.Get("X-Hub-Author"))
		sig, _ := base64.StdEncoding.DecodeString(r.Header.Get("X-Hub-Sig"))
		sum := sha256.Sum256(b)
		if len(pub) != ed25519.PublicKeySize || !ed25519.Verify(pub, []byte(hubUploadPrefix+r.Header.Get("X-Hub-Ts")+"\n"+hex.EncodeToString(sum[:])), sig) {
			w.WriteHeader(401)
			w.Write([]byte(`{"error":"bad upload signature"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(202)
		w.Write([]byte(`{"job":"abc","status":"queued","progress":0}`))
	}))
	defer hub.Close()

	s := &Server{StateDir: t.TempDir()}
	req := httptest.NewRequest("POST", "/v1/hub/media?hub="+url.QueryEscape(hub.URL), bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.handleHubMedia(w, req)
	if w.Code != 202 || w.Body.String() != `{"job":"abc","status":"queued","progress":0}` {
		t.Fatalf("relayed %d %s", w.Code, w.Body)
	}
	if gotLen != len(body) {
		t.Fatalf("hub got %d bytes, want %d", gotLen, len(body))
	}
	if left, _ := filepath.Glob(filepath.Join(s.StateDir, ".hub-media-*")); len(left) != 0 {
		t.Fatalf("staged files left behind: %v", left)
	}
	os.RemoveAll(s.StateDir)
}
