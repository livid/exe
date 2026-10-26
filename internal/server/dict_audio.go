package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
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
	if dictAudioComplete(k) || k.From != "en" || dictTTSURL() == "" {
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
		s.dictFillExampleAudio(context.Background(), &kept)
		f.kept = &kept
	}()
}

// Only the two English examples are spoken, never their translations.
func dictExampleTexts(k *dictKept) []string {
	if k.From != "en" {
		return nil
	}
	var e dictEntry
	if json.Unmarshal(k.Entry, &e) != nil {
		return nil
	}
	texts := make([]string, min(2, len(e.Examples)))
	for i := range texts {
		texts[i] = e.Examples[i].Text
	}
	return texts
}

func dictAudioComplete(k *dictKept) bool {
	if !k.Audio {
		return false
	}
	for i, text := range dictExampleTexts(k) {
		if strings.TrimSpace(text) != "" && (i >= len(k.ExampleAudio) || !k.ExampleAudio[i]) {
			return false
		}
	}
	return true
}

func dictReadExampleAudio(db *sql.DB, k *dictKept) error {
	texts := dictExampleTexts(k)
	k.ExampleAudio = make([]bool, len(texts))
	if len(texts) == 0 {
		return nil
	}
	rows, err := db.Query(`SELECT example,text FROM example_audio WHERE src=? AND dst=? AND key=?`, k.From, k.To, k.Q)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var i int
		var text string
		if err := rows.Scan(&i, &text); err != nil {
			return err
		}
		if i >= 0 && i < len(texts) && text == texts[i] && strings.TrimSpace(text) != "" {
			k.ExampleAudio[i] = true
		}
	}
	return rows.Err()
}

func (s *Server) dictFillExampleAudio(ctx context.Context, k *dictKept) {
	db, err := s.dictDatabase()
	if err != nil || dictReadExampleAudio(db, k) != nil {
		return
	}
	for i, text := range dictExampleTexts(k) {
		if k.ExampleAudio[i] || strings.TrimSpace(text) == "" || ctx.Err() != nil {
			continue
		}
		var current bool
		if db.QueryRow(`SELECT EXISTS(SELECT 1 FROM entries WHERE src=? AND dst=? AND key=? AND entry=?)`,
			k.From, k.To, k.Q, string(k.Entry)).Scan(&current) != nil || !current {
			break
		}
		mp3, err := dictSpeech(ctx, text)
		if err != nil {
			continue // keep successful clips; retry only missing ones on another lookup
		}
		// No transaction is held over TTS. Recheck the original entry in
		// the write and preserve matching audio saved by a racing backfill.
		_, _ = db.Exec(`INSERT INTO example_audio(src,dst,key,example,text,mp3,created)
			SELECT src,dst,key,?,?,?,? FROM entries WHERE src=? AND dst=? AND key=? AND entry=?
			ON CONFLICT(src,dst,key,example) DO UPDATE SET
			text=excluded.text,mp3=excluded.mp3,created=excluded.created
			WHERE example_audio.text<>excluded.text`, i, text, mp3, time.Now().UnixMilli(),
			k.From, k.To, k.Q, string(k.Entry))
	}
	_ = dictReadExampleAudio(db, k)
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
	if q.Has("example") {
		// Public numbering is 1 and 2, matching the order on the page.
		i, parseErr := strconv.Atoi(q.Get("example"))
		if parseErr != nil || i < 1 || i > 2 {
			writeErr(w, http.StatusBadRequest, errors.New("example must be 1 or 2"))
			return
		}
		err = db.QueryRowContext(r.Context(), `SELECT a.mp3,a.created FROM example_audio a
			JOIN entries e USING(src,dst,key) WHERE a.src=? AND a.dst=? AND a.key=? AND a.example=?
			AND a.text=json_extract(e.entry,'$.examples[' || a.example || '].text')`,
			src.Code, dst.Code, key, i-1).Scan(&mp3, &created)
	} else {
		err = db.QueryRowContext(r.Context(), `SELECT mp3, created FROM entry_audio WHERE src = ? AND dst = ? AND key = ?`,
			src.Code, dst.Code, key).Scan(&mp3, &created)
	}
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
	if q.Has("example") {
		w.Header().Set("Cache-Control", "private, no-cache")
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, "pronunciation.mp3", time.UnixMilli(created), bytes.NewReader(mp3))
}
