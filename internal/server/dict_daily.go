package server

import (
	"encoding/json"
	"net/http"
	"time"
)

type dictDailyWord struct {
	dictCandidate
	POS   string `json:"pos,omitempty"`
	Gloss string `json:"gloss,omitempty"`
}

// A daily preview only reads the dictionary. It never starts a session or
// counts as a lookup. The browser supplies its local calendar date; scripts
// that omit it get today's UTC date.
func (s *Server) handleDictDaily(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	src, dst, _, err := dictQuery(q.Get("from"), q.Get("to"), "daily")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	date := q.Get("date")
	if date == "" {
		date = time.Now().UTC().Format(time.DateOnly)
	}
	day, err := time.Parse(time.DateOnly, date)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	db, err := s.dictDatabase()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	// Hold the pool to entries saved before this date, so another lookup
	// today does not change its word. A brand-new dictionary starts with
	// its oldest entry until the next day brings that entry into the pool.
	var count int64
	err = db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM entries
		WHERE src = ? AND dst = ? AND created < ?`, src.Code, dst.Code, day.UnixMilli()).Scan(&count)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	query := `SELECT key, headword, entry FROM entries WHERE src = ? AND dst = ? ORDER BY created, key LIMIT 1`
	args := []any{src.Code, dst.Code}
	if count > 0 {
		offset := (day.Unix()/86400%count + count) % count
		query = `SELECT key, headword, entry FROM entries WHERE src = ? AND dst = ? AND created < ? ORDER BY key LIMIT 1 OFFSET ?`
		args = append(args, day.UnixMilli(), offset)
	}
	rows, err := db.QueryContext(r.Context(), query, args...)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()
	var word *dictDailyWord
	if rows.Next() {
		word = &dictDailyWord{}
		var raw string
		if err := rows.Scan(&word.Q, &word.Word, &raw); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		if dictKey(word.Word) != word.Q {
			word.Word = word.Q
		}
		var entry dictEntry
		if err := json.Unmarshal([]byte(raw), &entry); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		if len(entry.Senses) > 0 {
			word.POS, word.Gloss = entry.Senses[0].POS, entry.Senses[0].Gloss
		}
	}
	if err := rows.Err(); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"date": date, "word": word})
}
