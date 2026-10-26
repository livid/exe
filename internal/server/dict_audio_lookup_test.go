package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"exe/internal/config"
)

func savedDictAudioServer(t *testing.T, src, key string) (*Server, *httptest.Server) {
	t.Helper()
	s := New(&config.Config{}, nil, nil, "", t.TempDir())
	k := &dictKept{From: src, To: "zh", Q: key, Entry: json.RawMessage(dictAnswer), Model: "original-model", Effort: "xhigh", Created: 1234}
	if err := s.dictPut(k, "serendipity", nil); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(func() { ts.Close(); s.dictDB.Close() })
	old := dictCodexPath
	dictCodexPath = func() string { t.Error("saved pronunciation started Codex"); return "" }
	t.Cleanup(func() { dictCodexPath = old })
	return s, ts
}

func savedAudioLookup(t *testing.T, ts *httptest.Server, path, src, key string) dictKept {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	body, _ := json.Marshal(map[string]any{"from": src, "to": "zh", "q": key})
	req, _ := http.NewRequestWithContext(ctx, "POST", ts.URL+path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("saved lookup blocked on TTS: %v", err)
	}
	defer r.Body.Close()
	var reply struct {
		Entry dictKept `json:"entry"`
	}
	if err := json.NewDecoder(r.Body).Decode(&reply); err != nil || r.StatusCode != 200 || reply.Entry.Q == "" {
		t.Fatalf("lookup = %+v, HTTP %d, %v", reply, r.StatusCode, err)
	}
	return reply.Entry
}

func finishedAudio(t *testing.T, s *Server, fid string) *dictKept {
	t.Helper()
	var f *dictFlight
	s.dictMu.Lock()
	for _, candidate := range s.dictAll {
		if candidate.fid == fid {
			f = candidate
			break
		}
	}
	s.dictMu.Unlock()
	if f == nil {
		t.Fatalf("missing audio flight %q", fid)
	}
	timeout := time.NewTimer(3 * time.Second)
	defer timeout.Stop()
	for {
		f.mu.Lock()
		done, changed := f.done, f.changed
		f.mu.Unlock()
		if done {
			k, ok := f.final("")["entry"].(dictKept)
			if !ok {
				t.Fatal("audio flight lost definition")
			}
			return &k
		}
		select {
		case <-changed:
		case <-timeout.C:
			t.Fatal("audio flight did not finish")
		}
	}
}

func TestDictSavedAudioImmediateAndShared(t *testing.T) {
	mp3 := []byte("ID3saved headword")
	gate := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(gate) }) }
	var calls atomic.Int32
	tts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveDictTestExample(w, r) {
			return
		}
		calls.Add(1)
		if r.URL.Path != "/speak/serendipity" {
			t.Errorf("spoke query instead of headword: %s", r.URL)
		}
		<-gate
		w.Header().Set("Content-Type", "audio/mpeg")
		w.Write(mp3)
	}))
	defer tts.Close()
	defer release()
	t.Setenv("EXE_DICT_TTS_URL", tts.URL)
	s, ts := savedDictAudioServer(t, "en", "serendipities")
	s.dictPutTypo("en", "serendipitiesx", "serendipities")
	first := savedAudioLookup(t, ts, "/v1/dict/start", "en", "serendipitiesx")
	if first.AudioFlight == "" || first.Audio || first.Corrected != "serendipitiesx" || first.Q != "serendipities" {
		t.Fatalf("initial entry = %+v", first)
	}
	// Both lookup APIs return the text before the stalled TTS responds.
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			k := savedAudioLookup(t, ts, "/v1/dict", "en", "serendipities")
			if k.AudioFlight != first.AudioFlight {
				t.Errorf("duplicate flight: %s != %s", k.AudioFlight, first.AudioFlight)
			}
		}()
	}
	wg.Wait()
	release()
	k := finishedAudio(t, s, first.AudioFlight)
	if !k.Audio || k.AudioFlight != "" || k.Written || k.Model != "original-model" || k.Created != 1234 || string(k.Entry) != dictAnswer {
		t.Fatalf("completed entry = %+v", k)
	}
	if calls.Load() != 1 {
		t.Fatalf("TTS calls = %d", calls.Load())
	}
	cached := savedAudioLookup(t, ts, "/v1/dict/start", "en", "serendipities")
	if !cached.Audio || cached.AudioFlight != "" || calls.Load() != 1 {
		t.Fatalf("cached lookup regenerated audio: %+v", cached)
	}
	var stored []byte
	var looked int
	if err := s.dictDB.QueryRow(`SELECT mp3,lookups FROM entry_audio JOIN entries USING(src,dst,key)`).Scan(&stored, &looked); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stored, mp3) || looked != 11 {
		t.Fatalf("stored MP3=%q, lookups=%d", stored, looked)
	}
}

func TestDictSavedAudioFailureRetries(t *testing.T) {
	var calls atomic.Int32
	tts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveDictTestExample(w, r) {
			return
		}
		if calls.Add(1) == 1 {
			http.Error(w, "unavailable", 503)
			return
		}
		w.Header().Set("Content-Type", "audio/mpeg")
		w.Write([]byte("ID3retry"))
	}))
	defer tts.Close()
	t.Setenv("EXE_DICT_TTS_URL", tts.URL)
	s, ts := savedDictAudioServer(t, "en", "serendipity")
	first := savedAudioLookup(t, ts, "/v1/dict/start", "en", "serendipity")
	k := finishedAudio(t, s, first.AudioFlight)
	if k.Audio || k.AudioFlight != "" || string(k.Entry) != dictAnswer {
		t.Fatalf("failed fetch damaged entry: %+v", k)
	}
	second := savedAudioLookup(t, ts, "/v1/dict/start", "en", "serendipity")
	if second.AudioFlight == "" || second.AudioFlight == first.AudioFlight {
		t.Fatalf("did not retry: %+v", second)
	}
	if k = finishedAudio(t, s, second.AudioFlight); !k.Audio {
		t.Fatal("retry did not save audio")
	}
	if calls.Load() != 2 {
		t.Fatalf("TTS calls = %d", calls.Load())
	}
}

func TestDictSavedAudioRacingWriter(t *testing.T) {
	for _, changeEntry := range []bool{false, true} {
		t.Run(map[bool]string{false: "backfill", true: "changed-headword"}[changeEntry], func(t *testing.T) {
			gate, started := make(chan struct{}), make(chan struct{})
			var once sync.Once
			release := func() { once.Do(func() { close(gate) }) }
			tts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if serveDictTestExample(w, r) {
					return
				}
				close(started)
				<-gate
				w.Header().Set("Content-Type", "audio/mpeg")
				w.Write([]byte("ID3lookup"))
			}))
			defer tts.Close()
			defer release()
			t.Setenv("EXE_DICT_TTS_URL", tts.URL)
			s, ts := savedDictAudioServer(t, "en", "serendipity")
			k := savedAudioLookup(t, ts, "/v1/dict/start", "en", "serendipity")
			select {
			case <-started:
			case <-time.After(2 * time.Second):
				t.Fatal("TTS not started")
			}
			var err error
			if changeEntry {
				_, err = s.dictDB.Exec(`UPDATE entries SET headword='changed'`)
			} else {
				_, err = s.dictDB.Exec(`INSERT INTO entry_audio VALUES ('en','zh','serendipity',?,2)`, []byte("ID3backfill"))
			}
			if err != nil {
				t.Fatal(err)
			}
			release()
			final := finishedAudio(t, s, k.AudioFlight)
			if changeEntry {
				var count int
				s.dictDB.QueryRow(`SELECT COUNT(*) FROM entry_audio`).Scan(&count)
				if count != 0 || final.Audio {
					t.Fatal("saved stale pronunciation")
				}
			} else {
				var stored []byte
				s.dictDB.QueryRow(`SELECT mp3 FROM entry_audio`).Scan(&stored)
				if string(stored) != "ID3backfill" || !final.Audio {
					t.Fatalf("overwrote racing audio: %q", stored)
				}
			}
		})
	}
}

func TestDictSavedAudioPreviewAndUnsupportedDoNotFetch(t *testing.T) {
	var calls atomic.Int32
	tts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); http.Error(w, "unexpected", 500) }))
	defer tts.Close()
	t.Setenv("EXE_DICT_TTS_URL", tts.URL)
	s, ts := savedDictAudioServer(t, "de", "serendipity")
	if k := savedAudioLookup(t, ts, "/v1/dict/start", "de", "serendipity"); k.AudioFlight != "" {
		t.Fatal("non-English TTS started")
	}
	if _, err := s.dictDB.Exec(`UPDATE entries SET src='en'`); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/dict?from=en&to=zh&q=serendipity", "/v1/dict/audio?from=en&to=zh&q=serendipity"} {
		r, err := ts.Client().Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
	}
	s.dictMu.Lock()
	n := len(s.dictAll)
	s.dictMu.Unlock()
	if n != 0 || calls.Load() != 0 {
		t.Fatal("preview/playback started synthesis")
	}
	t.Setenv("EXE_DICT_TTS_URL", "")
	if k := savedAudioLookup(t, ts, "/v1/dict/start", "en", "serendipity"); k.AudioFlight != "" {
		t.Fatal("disabled TTS started")
	}
	// A missing MP3 must still be a normal saved definition without a model.
	k, err := s.dictGet("en", "zh", "serendipity")
	if err != nil || k == nil || !strings.Contains(string(k.Entry), "serendipity") {
		t.Fatalf("entry unavailable: %v", err)
	}
}
