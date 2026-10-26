package server

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"
	"time"

	"exe/internal/config"
)

func TestDictDaily(t *testing.T) {
	s := New(&config.Config{APIToken: "test-token"}, nil, nil, "", t.TempDir())
	db, err := s.dictDatabase()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	day, _ := time.Parse(time.DateOnly, "2026-10-26")
	insert := func(src, dst, key, headword string, created int64) {
		t.Helper()
		_, err := db.Exec(`INSERT INTO entries VALUES (?, ?, ?, ?, ?, 'test', 'xhigh', ?, 123, 0)`,
			src, dst, key, headword, `{"senses":[{"pos":"n.","gloss":"a saved definition"}]}`, created)
		if err != nil {
			t.Fatal(err)
		}
	}
	h := s.Handler()
	request := func(src, dst, date, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/v1/dict/daily?"+url.Values{"from": {src}, "to": {dst}, "date": {date}}.Encode(), nil)
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	read := func(src, dst, date string) *dictDailyWord {
		t.Helper()
		w := request(src, dst, date, "test-token")
		var got struct {
			Date string         `json:"date"`
			Word *dictDailyWord `json:"word"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != 200 || got.Date != date {
			t.Fatalf("response %d: %s (%v)", w.Code, w.Body, err)
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("daily word can be cached past the requested day")
		}
		return got.Word
	}
	if got := read("en", "zh", "2026-10-26"); got != nil {
		t.Fatalf("empty dictionary: %+v", got)
	}
	insert("en", "zh", "serene", "serene", day.Add(-time.Hour).UnixMilli())
	insert("en", "zh", "wander", "wander", day.Add(-time.Hour).UnixMilli())
	first := read("en", "zh", "2026-10-26")
	if first == nil || first.Gloss != "a saved definition" || first.POS != "n." {
		t.Fatalf("preview: %+v", first)
	}
	if got := read("en", "zh", "2026-10-27"); got.Q == first.Q {
		t.Fatal("next day kept the same word with two entries available")
	}
	insert("en", "zh", "added-today", "added-today", day.Add(time.Hour).UnixMilli())
	if got := read("en", "zh", "2026-10-26"); !reflect.DeepEqual(got, first) {
		t.Fatalf("new lookup changed today's word: %+v -> %+v", first, got)
	}
	insert("de", "zh", "ärger", "Ärger", day.Add(time.Hour).UnixMilli())
	insert("de", "zh", "abend", "Abend", day.Add(2*time.Hour).UnixMilli())
	if got := read("de", "zh", "2026-10-26"); got.Word != "Ärger" {
		t.Fatalf("brand-new dictionary should use its oldest entry: %+v", got)
	}
	insert("en", "ja", "went", "go", 1)
	if got := read("en", "ja", "2026-10-26"); got.Word != "went" || got.Q != "went" {
		t.Fatalf("pair / inflected form: %+v", got)
	}
	if got := read("fr", "zh", "2026-10-26"); got != nil {
		t.Fatalf("another language leaked: %+v", got)
	}
	for _, tc := range []struct{ src, dst, date string }{
		{"xx", "zh", "2026-10-26"}, {"en", "xx", "2026-10-26"}, {"en", "zh", "2026-02-30"}, {"en", "zh", "today"},
	} {
		if w := request(tc.src, tc.dst, tc.date, "test-token"); w.Code != 400 {
			t.Errorf("invalid query %+v: %d", tc, w.Code)
		}
	}
	if w := request("en", "zh", "", "test-token"); w.Code != 200 {
		t.Fatalf("default date: %d", w.Code)
	}
	if w := request("en", "zh", "2026-10-26", ""); w.Code != 401 {
		t.Fatalf("missing token: %d", w.Code)
	}
	var touched int
	if err := db.QueryRow(`SELECT COUNT(*) FROM entries WHERE looked != 123 OR lookups != 0`).Scan(&touched); err != nil || touched != 0 || len(s.dictAll) != 0 {
		t.Fatalf("preview started a lookup: touched=%d flights=%d err=%v", touched, len(s.dictAll), err)
	}
}
