package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"exe/internal/config"
)

// Word-audio tests keep counting headwords; examples have distinct bytes.
func serveDictTestExample(w http.ResponseWriter, r *http.Request) bool {
	for _, sentence := range dictExampleTexts(&dictKept{From: "en", Entry: json.RawMessage(dictAnswer)}) {
		if r.URL.Path == "/speak/"+sentence {
			w.Header().Set("Content-Type", "audio/mpeg")
			io.WriteString(w, "ID3"+sentence)
			return true
		}
	}
	return false
}

func TestDictExampleAudioSavedBeforeSlowSynthesis(t *testing.T) {
	fakeDictCodex(t, t.TempDir(), dictAnswer)
	s := New(&config.Config{}, nil, nil, "", t.TempDir())
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	defer func() { s.dictDB.Close() }()
	gate, started := make(chan struct{}), make(chan struct{})
	var releaseOnce, startOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(gate) }) }
	var calls atomic.Int32
	tts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/speak/serendipity" {
			startOnce.Do(func() { close(started) })
			<-gate
		}
		w.Header().Set("Content-Type", "audio/mpeg")
		io.WriteString(w, "ID3"+r.URL.Path)
	}))
	defer tts.Close()
	defer release()
	t.Setenv("EXE_DICT_TTS_URL", tts.URL)
	resp, err := ts.Client().Post(ts.URL+"/v1/dict/start", "application/json", strings.NewReader(`{"from":"en","to":"zh","q":"serendipity","exact":true}`))
	if err != nil {
		t.Fatal(err)
	}
	var reply struct {
		Flight string `json:"flight"`
	}
	err = json.NewDecoder(resp.Body).Decode(&reply)
	resp.Body.Close()
	if err != nil || reply.Flight == "" {
		t.Fatal("generation did not start")
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("example synthesis did not start")
	}
	// The definition has reached SQLite even while the first sentence stalls.
	k := savedAudioLookup(t, ts, "/v1/dict/start", "en", "serendipity")
	if k.AudioFlight != reply.Flight || !k.Audio || len(k.Entry) == 0 {
		t.Fatalf("lookup did not join generation: %+v", k)
	}
	release()
	final := finishedAudio(t, s, reply.Flight)
	if !dictAudioComplete(final) || !final.Written || calls.Load() != 3 {
		t.Fatalf("incomplete or duplicated synthesis: %+v, calls=%d", final, calls.Load())
	}
}

func TestDictExampleAudioPartialRetryAndShared(t *testing.T) {
	s, ts := savedDictAudioServer(t, "en", "serendipity")
	if _, err := s.dictDB.Exec(`INSERT INTO entry_audio VALUES('en','zh','serendipity',?,1)`, []byte("ID3word")); err != nil {
		t.Fatal(err)
	}
	var first, second atomic.Int32
	gate := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(gate) }) }
	tts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "It was pure") {
			first.Add(1)
			<-gate
		} else if strings.Contains(r.URL.Path, "Although") {
			if second.Add(1) == 1 {
				http.Error(w, "temporary failure", 503)
				return
			}
		} else {
			t.Errorf("unexpected speech: %s", r.URL.Path)
		}
		serveDictTestExample(w, r)
	}))
	defer tts.Close()
	defer release()
	t.Setenv("EXE_DICT_TTS_URL", tts.URL)
	k := savedAudioLookup(t, ts, "/v1/dict/start", "en", "serendipity")
	if !k.Audio || k.AudioFlight == "" || len(k.ExampleAudio) != 2 || k.ExampleAudio[0] {
		t.Fatalf("missing examples did not start a fetch: %+v", k)
	}
	joined := savedAudioLookup(t, ts, "/v1/dict", "en", "serendipity")
	if joined.AudioFlight != k.AudioFlight {
		t.Fatal("concurrent example fetch was not shared")
	}
	release()
	partial := finishedAudio(t, s, k.AudioFlight)
	if !partial.Audio || !partial.ExampleAudio[0] || partial.ExampleAudio[1] || string(partial.Entry) != dictAnswer {
		t.Fatalf("partial failure lost text or successful audio: %+v", partial)
	}
	k = savedAudioLookup(t, ts, "/v1/dict/start", "en", "serendipity")
	complete := finishedAudio(t, s, k.AudioFlight)
	if !dictAudioComplete(complete) || first.Load() != 1 || second.Load() != 2 {
		t.Fatalf("retry did not fetch only missing clip: %+v; calls %d/%d", complete, first.Load(), second.Load())
	}
	k = savedAudioLookup(t, ts, "/v1/dict/start", "en", "serendipity")
	if k.AudioFlight != "" || !dictAudioComplete(&k) {
		t.Fatal("complete audio fetched again")
	}
	for _, suffix := range []string{"0", "3", "-1", "x", ""} {
		r, err := ts.Client().Get(ts.URL + "/v1/dict/audio?from=en&to=zh&q=serendipity&example=" + suffix)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode != 400 {
			t.Fatalf("example %q = %d", suffix, r.StatusCode)
		}
	}
}

func TestDictExampleAudioRacingWriterAndChangedText(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "backfill", true: "edited-sentence"}[changed], func(t *testing.T) {
			s, ts := savedDictAudioServer(t, "en", "serendipity")
			if _, err := s.dictDB.Exec(`INSERT INTO entry_audio VALUES('en','zh','serendipity',?,1)`, []byte("ID3word")); err != nil {
				t.Fatal(err)
			}
			gate, started := make(chan struct{}), make(chan struct{})
			var once, startOnce sync.Once
			release := func() { once.Do(func() { close(gate) }) }
			tts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				startOnce.Do(func() { close(started) })
				<-gate
				w.Header().Set("Content-Type", "audio/mpeg")
				io.WriteString(w, "ID3lookup")
			}))
			defer tts.Close()
			defer release()
			t.Setenv("EXE_DICT_TTS_URL", tts.URL)
			k := savedAudioLookup(t, ts, "/v1/dict/start", "en", "serendipity")
			select {
			case <-started:
			case <-time.After(2 * time.Second):
				t.Fatal("TTS not started")
			}
			text := dictExampleTexts(&k)[0]
			if _, err := s.dictDB.Exec(`INSERT INTO example_audio VALUES('en','zh','serendipity',0,?,?,1)`, text, []byte("ID3backfill")); err != nil {
				t.Fatal(err)
			}
			if changed {
				if _, err := s.dictDB.Exec(`UPDATE entries SET entry=?`, strings.Replace(dictAnswer, text, "An entirely different sentence.", 1)); err != nil {
					t.Fatal(err)
				}
			}
			release()
			finishedAudio(t, s, k.AudioFlight)
			var audio []byte
			if err := s.dictDB.QueryRow(`SELECT mp3 FROM example_audio WHERE example=0`).Scan(&audio); err != nil {
				t.Fatal(err)
			}
			if string(audio) != "ID3backfill" {
				t.Fatal("overwrote racing audio")
			}
			if changed {
				current, err := s.dictGet("en", "zh", "serendipity")
				if err != nil || current.ExampleAudio[0] {
					t.Fatal("advertised stale audio")
				}
				r, err := ts.Client().Get(ts.URL + "/v1/dict/audio?from=en&to=zh&q=serendipity&example=1")
				if err != nil {
					t.Fatal(err)
				}
				r.Body.Close()
				if r.StatusCode != 404 {
					t.Fatal("played stale example")
				}
				// The next lookup replaces stale bytes only for the changed text.
				next := savedAudioLookup(t, ts, "/v1/dict/start", "en", "serendipity")
				if final := finishedAudio(t, s, next.AudioFlight); !dictAudioComplete(final) {
					t.Fatal("edited examples not repaired")
				}
			}
		})
	}
}
