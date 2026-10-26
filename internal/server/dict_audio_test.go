package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"exe/internal/config"
)

func TestDictAudioSavedWithGeneratedHeadword(t *testing.T) {
	dir := t.TempDir()
	fakeDictCodex(t, dir, dictAnswer)
	// The spoken text must be the headword, not the typed misspelling.
	if err := os.WriteFile(filepath.Join(dir, "spell.json"), []byte(`{"verdict":"typo","word":"serendipity","suggestions":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	mp3 := []byte("ID3\x04\x00\x00saved pronunciation")
	var calls atomic.Int32
	tts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveDictTestExample(w, r) {
			return
		}
		calls.Add(1)
		if r.URL.Path != "/speak/serendipity" || r.URL.Query().Get("v") != "Aiden" {
			t.Errorf("TTS request = %s", r.URL)
		}
		w.Header().Set("Content-Type", "audio/mpeg")
		w.Write(mp3)
	}))
	defer tts.Close()
	t.Setenv("EXE_DICT_TTS_URL", tts.URL)
	state := t.TempDir()
	s := New(&config.Config{}, nil, nil, "", state)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	lines := dictLines(t, dictPost(t, ts.URL, `{"from":"en","to":"zh","q":"serendipty"}`))
	k, _ := lines[len(lines)-1]["entry"].(map[string]any)
	if k == nil || len(k["example_audio"].([]any)) != 2 || k["example_audio"].([]any)[0] != true || k["example_audio"].([]any)[1] != true || k["audio"] != true || k["corrected"] != "serendipty" {
		t.Fatalf("generated entry = %v", k)
	}
	var saved []byte
	var typ string
	if err := s.dictDB.QueryRow(`SELECT mp3, typeof(mp3) FROM entry_audio WHERE src='en' AND dst='zh' AND key='serendipity'`).Scan(&saved, &typ); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(saved, mp3) || typ != "blob" {
		t.Fatalf("SQLite audio = %q (%s)", saved, typ)
	}
	// Reopen the database with the synthesis service gone. Both cached
	// lookup and playback must work, with normal API auth and byte ranges.
	tts.Close()
	ts.Close()
	s.dictDB.Close()
	s = New(&config.Config{APIToken: "test-token"}, nil, nil, "", state)
	defer func() { s.dictDB.Close() }()
	h := s.Handler()
	request := func(path, token, method, span string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		if span != "" {
			r.Header.Set("Range", span)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	lookup := request("/v1/dict?from=en&to=zh&q=serendipty", "test-token", "GET", "")
	var cached dictKept
	if err := json.Unmarshal(lookup.Body.Bytes(), &cached); err != nil || lookup.Code != 200 || !cached.Audio || cached.Corrected != "serendipty" {
		t.Fatalf("cached entry = %s (%v)", lookup.Body, err)
	}
	path := "/v1/dict/audio?from=en&to=zh&q=serendipity"
	if w := request(path, "", "GET", ""); w.Code != 401 {
		t.Fatalf("unauthenticated playback = %d", w.Code)
	}
	if w := request(path, "test-token", "GET", ""); w.Code != 200 || !bytes.Equal(w.Body.Bytes(), mp3) || w.Header().Get("Content-Type") != "audio/mpeg" {
		t.Fatalf("playback = %d %s", w.Code, w.Body)
	}
	if w := request(path, "test-token", "GET", "bytes=0-2"); w.Code != 206 || w.Body.String() != "ID3" {
		t.Fatalf("range playback = %d %s", w.Code, w.Body)
	}
	if w := request(path, "test-token", "HEAD", ""); w.Code != 200 || w.Body.Len() != 0 {
		t.Fatalf("HEAD playback = %d %s", w.Code, w.Body)
	}
	if w := request("/v1/dict/audio?from=en&to=ja&q=serendipity", "test-token", "GET", ""); w.Code != 404 {
		t.Fatalf("other language pair = %d", w.Code)
	}
	for i, sentence := range dictExampleTexts(&cached) {
		p := path + "&example=" + strconv.Itoa(i+1)
		if w := request(p, "test-token", "GET", ""); w.Code != 200 || w.Body.String() != "ID3"+sentence {
			t.Fatalf("example playback = %d %q", w.Code, w.Body)
		}
		if w := request(p, "", "GET", ""); w.Code != 401 {
			t.Fatal("example bypassed auth")
		}
		if w := request(p, "test-token", "GET", "bytes=0-2"); w.Code != 206 || w.Body.String() != "ID3" {
			t.Fatal("example range failed")
		}
		if w := request(p, "test-token", "HEAD", ""); w.Code != 200 || w.Body.Len() != 0 {
			t.Fatal("example HEAD failed")
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("syntheses = %d", calls.Load())
	}
}

func TestDictAudioFailureKeepsEntry(t *testing.T) {
	fakeDictCodex(t, t.TempDir(), dictAnswer)
	tts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "unavailable", 503) }))
	defer tts.Close()
	t.Setenv("EXE_DICT_TTS_URL", tts.URL)
	s := New(&config.Config{}, nil, nil, "", t.TempDir())
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	defer func() { s.dictDB.Close() }()
	lines := dictLines(t, dictPost(t, ts.URL, `{"from":"en","to":"zh","q":"serendipity"}`))
	k, _ := lines[len(lines)-1]["entry"].(map[string]any)
	if k == nil || k["written"] != true || k["audio"] == true {
		t.Fatalf("entry after failed synthesis = %v", k)
	}
	kept, err := s.dictGet("en", "zh", "serendipity")
	if err != nil || kept == nil || kept.Audio {
		t.Fatalf("kept entry = %v, %v", kept, err)
	}
}

func TestDictSpeechRejectsBadResponses(t *testing.T) {
	for _, tc := range []struct{ name, contentType, body string }{
		{"html", "text/html", "<html>error</html>"},
		{"empty", "audio/mpeg", ""},
		{"not-mp3", "audio/mpeg", `{"error":"failed"}`},
		{"oversized", "audio/mpeg", "ID3" + strings.Repeat("x", dictMaxMP3)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				io.WriteString(w, tc.body)
			}))
			defer tts.Close()
			t.Setenv("EXE_DICT_TTS_URL", tts.URL)
			if b, err := dictSpeech(context.Background(), "word"); err == nil || b != nil {
				t.Fatalf("accepted bad response: %d bytes, %v", len(b), err)
			}
		})
	}
	// A stalled service must respect cancellation of the overall lookup.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := dictSpeech(ctx, "word"); err == nil {
		t.Fatal("ignored cancelled lookup")
	}
}

func TestDictAudioMigratesExistingDatabase(t *testing.T) {
	state := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(state, "dict.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE entries (src TEXT, dst TEXT, key TEXT, headword TEXT, entry TEXT,
		model TEXT, effort TEXT, created INTEGER, looked INTEGER, lookups INTEGER, PRIMARY KEY(src,dst,key));
		INSERT INTO entries VALUES ('en','zh','serendipity','serendipity',?,'old-model','xhigh',1,1,1)`, dictAnswer)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	s := New(&config.Config{}, nil, nil, "", state)
	defer func() { s.dictDB.Close() }()
	k, err := s.dictGet("en", "zh", "serendipity")
	if err != nil || k == nil || k.Audio || k.Model != "old-model" || string(k.Entry) != dictAnswer {
		t.Fatalf("old entry changed: %v, %v", k, err)
	}
}
