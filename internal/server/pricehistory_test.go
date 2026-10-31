package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// The Solana app's day: a token's samples and its own moves, no other
// token's; an unknown token is refused; an empty state answers with
// empty lists, never null.
func TestPriceHistory(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	s := &Server{alerts: &alertState{
		Samples: map[string][]priceSample{
			"SOL":  {{T: now.Add(-2 * time.Minute).Unix(), P: 95}, {T: now.Add(-time.Minute).Unix(), P: 96}},
			"PUMP": {{T: now.Unix(), P: 0.004}},
		},
		Alerts: []alertRec{
			{ID: 1, T: now.Add(-time.Hour), Token: "PUMP", Window: "1h", Change: 6},
			{ID: 2, T: now.Add(-30 * time.Minute), Token: "SOL", Window: "24h", Change: -9.1, Price: 92.9, Title: "SOL −9.1% in 24 hours"},
		},
		LastOK: now,
	}}
	get := func(q string) (int, map[string]any) {
		rec := httptest.NewRecorder()
		s.handlePriceHistory(rec, httptest.NewRequest(http.MethodGet, "/v1/prices/history"+q, nil))
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("%s: not JSON: %v", q, err)
		}
		return rec.Code, out
	}
	code, out := get("?token=sol")
	if code != http.StatusOK || out["token"] != "SOL" {
		t.Fatalf("sol: %d %v", code, out)
	}
	if n := len(out["samples"].([]any)); n != 2 {
		t.Errorf("SOL has %d samples, want 2", n)
	}
	moves := out["moves"].([]any)
	if len(moves) != 1 || moves[0].(map[string]any)["title"] != "SOL −9.1% in 24 hours" {
		t.Errorf("SOL's moves: %v", moves)
	}
	if rule := out["rule"].(map[string]any); rule["hour"] != alertRules["SOL"].Hour {
		t.Errorf("rule: %v", rule)
	}
	if code, _ := get("?token=DOGE"); code != http.StatusBadRequest {
		t.Errorf("DOGE: %d, want 400", code)
	}
	if code, out := get(""); code != http.StatusOK || out["token"] != "SOL" {
		t.Errorf("no token: %d %v, want SOL", code, out["token"])
	}
	empty := &Server{}
	rec := httptest.NewRecorder()
	empty.handlePriceHistory(rec, httptest.NewRequest(http.MethodGet, "/v1/prices/history?token=MET", nil))
	var out2 struct {
		Samples []priceSample `json:"samples"`
		Moves   []alertRec    `json:"moves"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out2); err != nil || out2.Samples == nil || out2.Moves == nil {
		t.Errorf("empty state: %s (%v)", rec.Body.String(), err)
	}
}
