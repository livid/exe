package server

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const dictMaxMP3 = 2 << 20

// A lookup returns the saved definition immediately. Its audio flight runs
// independently of the request and shares the existing stream, so closing
// the tab does not lose the MP3 and concurrent lookups do not synthesize it
// twice. GET previews and playback remain reads; a failed fetch is retried
// only by another lookup.
func (s *Server) dictFetchAudio(k *dictKept) {
	if k.Audio || k.From != "en" || dictTTSURL() == "" {
		return
	}
	id := "audio\x00" + k.From + "\x00" + k.To + "\x00" + k.Q
	s.dictMu.Lock()
	defer s.dictMu.Unlock()
	if f := s.dictFlights[id]; f != nil {
		k.AudioFlight = f.fid
		return
	}
	if s.dictFlights == nil {
		s.dictFlights = map[string]*dictFlight{}
	}
	if s.dictBoot == "" {
		s.dictBoot = strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	s.dictSeq++
	f := &dictFlight{began: time.Now(), changed: make(chan struct{}), ids: []string{id},
		fid: s.dictBoot + "-" + strconv.Itoa(s.dictSeq), from: k.From, to: k.To, word: k.Q, poke: s.dictPoke}
	s.dictFlights[id] = f
	s.dictAll = append(s.dictAll, f)
	kept := *k // the worker owns its result; never mutate the response being encoded
	kept.AudioFlight = ""
	k.AudioFlight = f.fid
	go func() {
		defer func() {
			s.dictMu.Lock()
			delete(s.dictFlights, id)
			f.ended = time.Now()
			s.dictMu.Unlock()
			f.finish()
		}()
		// Failure leaves the saved definition intact, without a sticky
		// failed job that could prevent the next lookup from retrying.
		kept.Audio = s.dictFillAudio(&kept)
		f.kept = &kept
	}()
}

func (s *Server) dictFillAudio(k *dictKept) bool {
	db, err := s.dictDatabase()
	if err != nil {
		return false
	}
	var headword string
	var exists bool
	err = db.QueryRow(`SELECT headword,
		EXISTS (SELECT 1 FROM entry_audio a WHERE a.src=e.src AND a.dst=e.dst AND a.key=e.key)
		FROM entries e WHERE src=? AND dst=? AND key=? AND entry=?`,
		k.From, k.To, k.Q, string(k.Entry)).Scan(&headword, &exists)
	if err != nil || strings.TrimSpace(headword) == "" {
		return false
	}
	if exists {
		return true
	}
	mp3, err := dictSpeech(context.Background(), headword)
	if err != nil {
		return false
	}
	// Recheck the entry in the write itself. A backfill may have supplied
	// audio meanwhile, or another writer may have changed the headword.
	// Never replace their audio or alter the entry and its metadata.
	_, err = db.Exec(`INSERT INTO entry_audio (src,dst,key,mp3,created)
		SELECT src,dst,key,?,? FROM entries
		WHERE src=? AND dst=? AND key=? AND headword=? AND entry=?
		ON CONFLICT(src,dst,key) DO NOTHING`, mp3, time.Now().UnixMilli(),
		k.From, k.To, k.Q, headword, string(k.Entry))
	if err != nil {
		return false
	}
	err = db.QueryRow(`SELECT EXISTS (SELECT 1 FROM entries e JOIN entry_audio a
		ON a.src=e.src AND a.dst=e.dst AND a.key=e.key
		WHERE e.src=? AND e.dst=? AND e.key=? AND e.headword=? AND e.entry=?)`,
		k.From, k.To, k.Q, headword, string(k.Entry)).Scan(&exists)
	return err == nil && exists
}

// An explicit empty value disables synthesis on hosts without TTS.
func dictTTSURL() string {
	if u, ok := os.LookupEnv("EXE_DICT_TTS_URL"); ok {
		return strings.TrimRight(strings.TrimSpace(u), "/")
	}
	return "http://127.0.0.1:20262"
}

// dictSpeech bounds both the wait (including a cold model load) and the
// response size. The MP3 stays out of the JSON entry and shared streams.
func dictSpeech(ctx context.Context, headword string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		dictTTSURL()+"/speak/"+url.PathEscape(headword)+"?v=Aiden", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "audio/mpeg")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("TTS: HTTP %d", resp.StatusCode)
	}
	if typ, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type")); typ != "audio/mpeg" {
		return nil, errors.New("TTS did not return an MP3")
	}
	mp3, err := io.ReadAll(io.LimitReader(resp.Body, dictMaxMP3+1))
	if err != nil {
		return nil, err
	}
	// An ID3 tag or MPEG frame sync, not a successful HTML/JSON response.
	if len(mp3) < 3 || len(mp3) > dictMaxMP3 ||
		(!bytes.HasPrefix(mp3, []byte("ID3")) && !(mp3[0] == 0xff && mp3[1]&0xe0 == 0xe0)) {
		return nil, errors.New("TTS returned an invalid or oversized MP3")
	}
	return mp3, nil
}

// Playback only reads SQLite: it works without TTS and never regenerates
// speech. The caller uses the canonical key from the returned entry.
func (s *Server) handleDictAudio(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	src, dst, key, err := dictQuery(q.Get("from"), q.Get("to"), q.Get("q"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	db, err := s.dictDatabase()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	var mp3 []byte
	var created int64
	err = db.QueryRowContext(r.Context(), `SELECT mp3, created FROM entry_audio WHERE src = ? AND dst = ? AND key = ?`,
		src.Code, dst.Code, key).Scan(&mp3, &created)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, errors.New("no pronunciation kept"))
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "audio/mpeg")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, "pronunciation.mp3", time.UnixMilli(created), bytes.NewReader(mp3))
}
