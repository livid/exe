package server

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"exe/internal/config"
)

func TestDictSuggestions(t *testing.T) {
	s := New(&config.Config{APIToken: "test-token"}, nil, nil, "", t.TempDir())
	db, err := s.dictDatabase()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	insert := func(src, dst, key, headword string) {
		t.Helper()
		_, err := db.Exec(`INSERT INTO entries VALUES (?, ?, ?, ?, '{}', 'test', 'xhigh', 1, 1, 0)`, src, dst, key, headword)
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, word := range []string{"serve", "serene", "serendipity", "series", "br[acket", "br*star", "br?question", "br%percent", "br_under"} {
		insert("en", "zh", word, word)
	}
	insert("en", "zh", "went", "go")
	insert("de", "zh", "ärger", "Ärger")
	insert("en", "ja", "serious", "serious")
	insert("fr", "zh", "serpent", "serpent")
	for i := range 12 {
		insert("en", "zh", fmt.Sprintf("prefix%02d", i), fmt.Sprintf("prefix%02d", i))
	}
	h := s.Handler()
	request := func(src, dst, q, token string) *httptest.ResponseRecorder {
		params := url.Values{"from": {src}, "to": {dst}, "q": {q}}
		r := httptest.NewRequest("GET", "/v1/dict/suggest?"+params.Encode(), nil)
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	for _, tc := range []struct {
		src, dst, q string
		want        []dictCandidate
	}{
		{"en", "zh", "  SER ", []dictCandidate{{"serendipity", "serendipity"}, {"serene", "serene"}, {"series", "series"}, {"serve", "serve"}}},
		{"en", "ja", "ser", []dictCandidate{{"serious", "serious"}}},
		{"fr", "zh", "ser", []dictCandidate{{"serpent", "serpent"}}},
		{"de", "zh", "ÄR", []dictCandidate{{"ärger", "Ärger"}}},
		{"en", "zh", "wen", []dictCandidate{{"went", "went"}}},
		{"en", "zh", "br[", []dictCandidate{{"br[acket", "br[acket"}}},
		{"en", "zh", "br*", []dictCandidate{{"br*star", "br*star"}}},
		{"en", "zh", "br?", []dictCandidate{{"br?question", "br?question"}}},
		{"en", "zh", "br%", []dictCandidate{{"br%percent", "br%percent"}}},
		{"en", "zh", "br_", []dictCandidate{{"br_under", "br_under"}}},
		{"en", "zh", "absent", []dictCandidate{}},
	} {
		t.Run(tc.src+"/"+tc.dst+"/"+tc.q, func(t *testing.T) {
			w := request(tc.src, tc.dst, tc.q, "test-token")
			var got struct {
				Suggestions []dictCandidate `json:"suggestions"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != 200 {
				t.Fatalf("response %d: %s (%v)", w.Code, w.Body, err)
			}
			if !reflect.DeepEqual(got.Suggestions, tc.want) {
				t.Errorf("suggestions = %v, want %v", got.Suggestions, tc.want)
			}
		})
	}
	var limited struct {
		Suggestions []dictCandidate `json:"suggestions"`
	}
	w := request("en", "zh", "prefix", "test-token")
	if err := json.Unmarshal(w.Body.Bytes(), &limited); err != nil || len(limited.Suggestions) != 8 || limited.Suggestions[7].Q != "prefix07" {
		t.Fatalf("limit/order: %s, %v", w.Body, err)
	}
	for _, q := range []string{"", strings.Repeat("x", 81), "abc\x00"} {
		if w := request("en", "zh", q, "test-token"); w.Code != 400 {
			t.Errorf("invalid query %q: %d", q, w.Code)
		}
	}
	if w := request("xx", "zh", "ser", "test-token"); w.Code != 400 {
		t.Errorf("invalid language: %d", w.Code)
	}
	if w := request("en", "zh", "ser", ""); w.Code != 401 {
		t.Errorf("no token: %d", w.Code)
	}
	var lookups int
	if err := db.QueryRow(`SELECT SUM(lookups) FROM entries`).Scan(&lookups); err != nil || lookups != 0 || len(s.dictAll) != 0 {
		t.Fatalf("autocomplete started a lookup: count=%d, flights=%d, err=%v", lookups, len(s.dictAll), err)
	}
}
