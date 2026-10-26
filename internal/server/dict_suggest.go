package server

import (
	"net/http"
	"strings"
)

type dictCandidate struct {
	Q    string `json:"q"`
	Word string `json:"word"`
}

// Autocomplete reads only saved entries in the selected dictionary. It
// neither starts a model session nor counts a suggestion as a lookup.
func (s *Server) handleDictSuggest(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	src, dst, prefix, err := dictQuery(q.Get("from"), q.Get("to"), q.Get("q"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	db, err := s.dictDatabase()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	// GLOB uses the entries primary key's binary ordering for a prefix
	// search. Its metacharacters in the reader's text stay literal.
	pattern := strings.NewReplacer("[", "[[]", "*", "[*]", "?", "[?]").Replace(prefix) + "*"
	rows, err := db.QueryContext(r.Context(), `SELECT key, headword FROM entries
		WHERE src = ? AND dst = ? AND key GLOB ? ORDER BY key LIMIT 8`, src.Code, dst.Code, pattern)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()
	candidates := []dictCandidate{}
	for rows.Next() {
		var c dictCandidate
		if err := rows.Scan(&c.Q, &c.Word); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		// Keep a headword's accents and capitals, but show an inflected
		// lookup as itself: "went" must not appear as "go" under "wen".
		if dictKey(c.Word) != c.Q {
			c.Word = c.Q
		}
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"suggestions": candidates})
}
