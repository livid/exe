package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Candles for the Solana app — Coinbase Exchange's public candles, served
// through the daemon so every open desktop shares one fetch and one
// cache, and the browser never goes to Coinbase itself (prices.go says
// why).
//
// GET /v1/prices/candles?token=SOL&tf=1h&end=<unix seconds> answers one
// page: the 300 candles of that timeframe up to and including the one
// holding end (now, when end is not given), oldest first, each [time,
// open, high, low, close, volume]. 4h is not a granularity Coinbase has:
// its page is 150 candles folded from 600 of the hour's, four to one on
// four-hour UTC boundaries. A page from before the pair's listing is
// empty, which tells the app it has reached the beginning. The page that
// holds now is kept 30 seconds; a closed page never changes and is kept
// six hours.

var coinbaseCandlesURL = "https://api.exchange.coinbase.com/products/%s/candles?granularity=%d&start=%s&end=%s"

type candleTF struct {
	Step int64 // seconds a candle covers
	Gran int64 // the Coinbase granularity fetched
	Page int   // candles in a page
}

var candleTFs = map[string]candleTF{
	"1m": {60, 60, 300}, "5m": {300, 300, 300}, "15m": {900, 900, 300},
	"1h": {3600, 3600, 300}, "4h": {14400, 3600, 150}, "1d": {86400, 86400, 300},
}
var candleTFOrder = []string{"1m", "5m", "15m", "1h", "4h", "1d"}

// candle is [time, open, high, low, close, volume] — Coinbase's own row
// reordered so a reader meets the price in the order charts say it.
type candle [6]float64

type candlesEntry struct {
	at  time.Time
	res map[string]any
}

const (
	candlesLiveTTL  = 30 * time.Second
	candlesPastTTL  = 6 * time.Hour
	candlesCacheMax = 240
)

func (s *Server) handleCandles(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	tok := strings.ToUpper(strings.TrimSpace(q.Get("token")))
	if tok == "" {
		tok = "SOL"
	}
	if _, ok := alertRules[tok]; !ok {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("token is one of %s", strings.Join(alertTokens, ", ")))
		return
	}
	tfName := q.Get("tf")
	if tfName == "" {
		tfName = "1h"
	}
	tf, ok := candleTFs[tfName]
	if !ok {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("tf is one of %s", strings.Join(candleTFOrder, ", ")))
		return
	}
	now := time.Now().Unix()
	end := now
	if v := q.Get("end"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("end is a unix time in seconds"))
			return
		}
		if n < now {
			end = n
		}
	}
	end -= end % tf.Step // the candle holding end
	live := end+tf.Step > now
	ttl := candlesPastTTL
	if live {
		ttl = candlesLiveTTL
	}
	key := tok + "|" + tfName + "|" + strconv.FormatInt(end, 10)
	s.candlesMu.Lock()
	e, had := s.candles[key]
	s.candlesMu.Unlock()
	if had && time.Since(e.at) < ttl {
		writeJSON(w, http.StatusOK, e.res)
		return
	}
	start := end - int64(tf.Page-1)*tf.Step
	cs, err := fetchCandles(r.Context(), tok+"-USD", tf.Gran, start, end+tf.Step-1)
	if err != nil {
		if had { // a stale page over none
			writeJSON(w, http.StatusOK, e.res)
			return
		}
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	if tf.Gran != tf.Step {
		cs = aggregateCandles(cs, tf.Step)
	}
	res := map[string]any{"token": tok, "tf": tfName, "step": tf.Step, "start": start, "end": end,
		"candles": cs, "live": live, "fetched_at": time.Now().UTC()}
	s.candlesMu.Lock()
	if s.candles == nil {
		s.candles = map[string]candlesEntry{}
	}
	for k, v := range s.candles { // stale pages go; a full cache drops its oldest
		if time.Since(v.at) > candlesPastTTL {
			delete(s.candles, k)
		}
	}
	for len(s.candles) >= candlesCacheMax {
		oldest, oldestAt := "", time.Now()
		for k, v := range s.candles {
			if v.at.Before(oldestAt) {
				oldest, oldestAt = k, v.at
			}
		}
		delete(s.candles, oldest)
	}
	s.candles[key] = candlesEntry{at: time.Now(), res: res}
	s.candlesMu.Unlock()
	writeJSON(w, http.StatusOK, res)
}

// fetchCandles is every gran candle whose time lies in [from, to] (unix
// seconds), oldest first, fetched in Coinbase's pages of 300.
func fetchCandles(ctx context.Context, product string, gran, from, to int64) ([]candle, error) {
	var out []candle
	span := 300 * gran
	for a := from; a <= to; a += span {
		b := a + span - 1
		if b > to {
			b = to
		}
		url := fmt.Sprintf(coinbaseCandlesURL, product, gran,
			time.Unix(a, 0).UTC().Format(time.RFC3339), time.Unix(b, 0).UTC().Format(time.RFC3339))
		body, err := coinbaseGet(ctx, url)
		if err != nil {
			return nil, err
		}
		var rows [][6]float64 // time, low, high, open, close, volume; newest first
		if err := json.Unmarshal(body, &rows); err != nil {
			return nil, fmt.Errorf("coinbase candles: %w", err)
		}
		for _, r := range rows {
			out = append(out, candle{r[0], r[3], r[2], r[1], r[4], r[5]})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	kept := out[:0]
	for i, c := range out { // a candle once, should two pages both hold it
		if i == 0 || c[0] != out[i-1][0] {
			kept = append(kept, c)
		}
	}
	if kept == nil {
		kept = []candle{}
	}
	return kept, nil
}

// aggregateCandles folds candles, oldest first, into ones step seconds
// wide on UTC boundaries: the first open, the highest high, the lowest
// low, the last close, the volumes added. A bucket still filling is a
// candle too, as Coinbase's own newest is.
func aggregateCandles(cs []candle, step int64) []candle {
	out := []candle{}
	for _, c := range cs {
		t := int64(c[0]) - int64(c[0])%step
		if n := len(out); n > 0 && int64(out[n-1][0]) == t {
			o := &out[n-1]
			if c[2] > o[2] {
				o[2] = c[2]
			}
			if c[3] < o[3] {
				o[3] = c[3]
			}
			o[4] = c[4]
			o[5] += c[5]
			continue
		}
		out = append(out, candle{float64(t), c[1], c[2], c[3], c[4], c[5]})
	}
	return out
}
