package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"exe/internal/config"
)

// The price module asks the daemon, which asks Coinbase once a minute for
// all the pairs together: spot for every pair, 24h stats where the pair has
// an order book, an error per pair that Coinbase does not know, and a 400
// for anything that is not a BASE-QUOTE pair.
func TestPricesEndpoint(t *testing.T) {
	var hits atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.URL.Path {
		case "/spot/SOL-USD":
			fmt.Fprint(w, `{"data":{"amount":"97.16","base":"SOL","currency":"USD"}}`)
		case "/spot/PUMP-SOL":
			fmt.Fprint(w, `{"data":{"amount":"0.00003617043173982414495","base":"PUMP","currency":"SOL"}}`)
		case "/stats/SOL-USD":
			fmt.Fprint(w, `{"open":"101.3","high":"101.57","low":"95.71","last":"97.05","volume":"1294067.55"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"message":"NotFound"}`)
		}
	}))
	defer up.Close()
	oldSpot, oldStats := coinbaseSpotURL, coinbaseStatsURL
	coinbaseSpotURL, coinbaseStatsURL = up.URL+"/spot/%s", up.URL+"/stats/%s"
	defer func() { coinbaseSpotURL, coinbaseStatsURL = oldSpot, oldStats }()

	s := New(&config.Config{}, nil, nil, "", t.TempDir())
	get := func(q string) (*httptest.ResponseRecorder, map[string]any) {
		rec := httptest.NewRecorder()
		s.handlePrices(rec, httptest.NewRequest("GET", "/v1/prices"+q, nil))
		var body map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return rec, body
	}

	rec, body := get("?pairs=sol-usd,PUMP-SOL,FOO-USD,SOL-USD")
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body)
	}
	prices := body["prices"].(map[string]any)
	sol := prices["SOL-USD"].(map[string]any)
	if sol["spot"] != 97.16 || sol["open"] != 101.3 || sol["low"] != 95.71 {
		t.Errorf("SOL-USD: %v", sol)
	}
	pump := prices["PUMP-SOL"].(map[string]any)
	if pump["spot"].(float64) < 0.000036 || pump["spot"].(float64) > 0.000037 || pump["open"] != nil {
		t.Errorf("PUMP-SOL: want a spot and no stats, got %v", pump)
	}
	foo := prices["FOO-USD"].(map[string]any)
	if foo["spot"] != nil || !strings.Contains(fmt.Sprint(foo["error"]), "does not quote") {
		t.Errorf("FOO-USD: want an error, got %v", foo)
	}
	if body["checked_at"] == nil {
		t.Error("no checked_at")
	}
	n := hits.Load()
	if n != 6 { // two calls per pair, the duplicate SOL-USD folded away
		t.Errorf("upstream hits: want 6, got %d", n)
	}

	// the same list inside the TTL is answered from the cache; force refetches
	if rec, _ := get("?pairs=SOL-USD,PUMP-SOL,FOO-USD"); rec.Code != http.StatusOK || hits.Load() != n {
		t.Errorf("cache miss: %d hits", hits.Load())
	}
	if rec, _ := get("?pairs=SOL-USD,PUMP-SOL,FOO-USD&force=1"); rec.Code != http.StatusOK || hits.Load() != n+6 {
		t.Errorf("force: %d hits", hits.Load())
	}

	many := make([]string, 13)
	for i := range many {
		many[i] = fmt.Sprintf("T%d-USD", i)
	}
	for _, q := range []string{"", "?pairs=", "?pairs=SOL", "?pairs=SOL-USD/../x", "?pairs=" + strings.Join(many, ",")} {
		if rec, _ := get(q); rec.Code != http.StatusBadRequest {
			t.Errorf("%q: want 400, got %d", q, rec.Code)
		}
	}
}
