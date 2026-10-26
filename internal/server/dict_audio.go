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
	"strings"
	"time"
)

const dictMaxMP3 = 2 << 20

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
