package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Four hour candles from the hour's: the first open, the highest high, the
// lowest low, the last close, the volumes added, on UTC boundaries.
func TestAggregateCandles(t *testing.T) {
	h := func(t int64, o, hi, lo, c, v float64) candle { return candle{float64(t), o, hi, lo, c, v} }
	in := []candle{
		h(14400*10+0, 10, 12, 9, 11, 1), h(14400*10+3600, 11, 15, 10, 14, 2), h(14400*10+7200, 14, 14, 8, 9, 3), h(14400*10+10800, 9, 10, 9, 10, 4),
		h(14400*11+0, 10, 11, 10, 11, 5),
	}
	out := aggregateCandles(in, 14400)
	if len(out) != 2 {
		t.Fatalf("got %d candles, want 2: %v", len(out), out)
	}
	if want := (candle{14400 * 10, 10, 15, 8, 10, 10}); out[0] != want {
		t.Errorf("first: %v, want %v", out[0], want)
	}
	if want := (candle{14400 * 11, 10, 11, 10, 11, 5}); out[1] != want {
		t.Errorf("second (still filling): %v, want %v", out[1], want)
	}
}

// The handler: a page is 300 candles ending at the bucket holding end,
// oldest first, reordered to open-high-low-close; 4h is two fetches of
// the hour's; the page is cached; a bad timeframe is refused.
func TestCandlesHandler(t *testing.T) {
	calls := 0
	var seen []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		seen = append(seen, r.URL.RawQuery)
		gran, _ := time.ParseDuration(r.URL.Query().Get("granularity") + "s")
		from, _ := time.Parse(time.RFC3339, r.URL.Query().Get("start"))
		to, _ := time.Parse(time.RFC3339, r.URL.Query().Get("end"))
		var rows [][6]float64
		for x := to.Truncate(gran); !x.Before(from); x = x.Add(-gran) { // newest first, as Coinbase answers
			p := float64(x.Unix()%1000) + 1
			rows = append(rows, [6]float64{float64(x.Unix()), p - 1, p + 2, p, p + 1, 7}) // time, low, high, open, close, volume
		}
		json.NewEncoder(w).Encode(rows)
	}))
	defer ts.Close()
	was := coinbaseCandlesURL
	coinbaseCandlesURL = ts.URL + "/%s/candles?granularity=%d&start=%s&end=%s"
	defer func() { coinbaseCandlesURL = was }()

	s := &Server{}
	get := func(q string) (int, map[string]any) {
		rec := httptest.NewRecorder()
		s.handleCandles(rec, httptest.NewRequest(http.MethodGet, "/v1/prices/candles"+q, nil))
		var out map[string]any
		json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	end := int64(1793466000) + 1800 // half past the hour
	code, out := get(fmt.Sprintf("?token=sol&tf=1h&end=%d", end))
	if code != http.StatusOK || out["token"] != "SOL" || out["tf"] != "1h" {
		t.Fatalf("1h: %d %v", code, out)
	}
	cs := out["candles"].([]any)
	if len(cs) != 300 {
		t.Fatalf("1h page: %d candles, want 300", len(cs))
	}
	last := cs[299].([]any)
	if int64(last[0].(float64)) != 1793466000 || out["end"].(float64) != 1793466000 {
		t.Errorf("the page ends at the bucket holding end: last %v, end %v", last[0], out["end"])
	}
	first := cs[0].([]any)
	if int64(first[0].(float64)) != 1793466000-299*3600 || out["start"].(float64) != float64(1793466000-299*3600) {
		t.Errorf("the page starts 299 candles earlier: %v, start %v", first[0], out["start"])
	}
	// reordered: open, high, low, close
	p := float64(1793466000%1000) + 1
	if last[1].(float64) != p || last[2].(float64) != p+2 || last[3].(float64) != p-1 || last[4].(float64) != p+1 {
		t.Errorf("order: %v, want open %v high %v low %v close %v", last[1:5], p, p+2, p-1, p+1)
	}
	if calls != 1 {
		t.Errorf("1h page took %d fetches, want 1", calls)
	}
	if code, _ := get(fmt.Sprintf("?token=sol&tf=1h&end=%d", end)); code != http.StatusOK || calls != 1 {
		t.Errorf("the page is cached: %d fetches", calls)
	}
	calls = 0
	code, out = get(fmt.Sprintf("?token=PUMP&tf=4h&end=%d", end))
	cs = out["candles"].([]any)
	if code != http.StatusOK || len(cs) != 150 || calls != 2 {
		t.Errorf("4h: %d, %d candles (want 150), %d fetches (want 2)", code, len(cs), calls)
	}
	if last := cs[149].([]any); int64(last[0].(float64)) != 1793466000-1793466000%14400 {
		t.Errorf("4h page ends on a four-hour boundary: %v", last[0])
	}
	if code, _ := get("?token=sol&tf=3h"); code != http.StatusBadRequest {
		t.Errorf("3h: %d, want 400", code)
	}
	if code, _ := get("?token=DOGE"); code != http.StatusBadRequest {
		t.Errorf("DOGE: %d, want 400", code)
	}
	if !strings.Contains(seen[0], "granularity=3600") {
		t.Errorf("the first fetch: %s", seen[0])
	}
}
