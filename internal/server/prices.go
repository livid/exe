package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The Control Strip's price module. The desktop asks the daemon, not
// Coinbase, so every open desktop on the node shares one answer a minute
// instead of each tab polling on its own, and the strip keeps working
// should Coinbase ever close its CORS door. Coinbase's public spot price
// API quotes any pair it knows, cross rates such as PUMP-SOL included; the
// Exchange stats endpoint adds the 24-hour open for the pairs that are real
// order books there (a cross pair is not, and simply has no stats).
var (
	coinbaseSpotURL  = "https://api.coinbase.com/v2/prices/%s/spot"
	coinbaseStatsURL = "https://api.exchange.coinbase.com/products/%s/stats"
	pricesClient     = &http.Client{Timeout: 10 * time.Second}
)

const (
	pricesTTL      = 60 * time.Second // Coinbase's own cache hint for spot prices
	pricesMaxPairs = 12
)

var pricePairRe = regexp.MustCompile(`^[A-Z0-9]{2,10}-[A-Z0-9]{2,10}$`)

// priceQuote is one pair's answer. Spot is the Coinbase spot price; Open,
// High, Low and Volume are the Exchange's 24-hour stats when the pair
// trades there. Error carries an upstream failure for that pair alone, so
// one unknown symbol never blanks the others.
type priceQuote struct {
	Spot   float64 `json:"spot,omitempty"`
	Open   float64 `json:"open,omitempty"`
	High   float64 `json:"high,omitempty"`
	Low    float64 `json:"low,omitempty"`
	Volume float64 `json:"volume,omitempty"`
	Error  string  `json:"error,omitempty"`
}

type pricesEntry struct {
	at  time.Time
	res map[string]any
}

// parsePricePairs reads ?pairs=SOL-USD,PUMP-USD — upper-cased, de-duplicated,
// each one BASE-QUOTE — and refuses anything else, so the daemon never
// becomes a general proxy.
func parsePricePairs(raw string) ([]string, error) {
	var pairs []string
	seen := map[string]bool{}
	for _, p := range strings.Split(raw, ",") {
		p = strings.ToUpper(strings.TrimSpace(p))
		if p == "" || seen[p] {
			continue
		}
		if !pricePairRe.MatchString(p) {
			return nil, fmt.Errorf("bad pair %q (want BASE-QUOTE, like SOL-USD)", p)
		}
		seen[p] = true
		pairs = append(pairs, p)
	}
	if len(pairs) == 0 {
		return nil, errors.New("pairs is required, like ?pairs=SOL-USD,PUMP-USD")
	}
	if len(pairs) > pricesMaxPairs {
		return nil, fmt.Errorf("at most %d pairs per request", pricesMaxPairs)
	}
	return pairs, nil
}

func (s *Server) handlePrices(w http.ResponseWriter, r *http.Request) {
	pairs, err := parsePricePairs(r.URL.Query().Get("pairs"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	key := strings.Join(pairs, ",")
	force := r.URL.Query().Get("force") != ""

	s.pricesMu.Lock()
	if e, ok := s.prices[key]; ok && !force && time.Since(e.at) < pricesTTL {
		s.pricesMu.Unlock()
		writeJSON(w, http.StatusOK, e.res)
		return
	}
	s.pricesMu.Unlock()

	quotes := fetchPrices(r.Context(), pairs)
	res := map[string]any{"checked_at": time.Now().UTC(), "prices": quotes}

	s.pricesMu.Lock()
	if s.prices == nil {
		s.prices = map[string]pricesEntry{}
	}
	for k, e := range s.prices { // a desktop that changed its list leaves no stale entry behind
		if time.Since(e.at) > 10*pricesTTL {
			delete(s.prices, k)
		}
	}
	s.prices[key] = pricesEntry{at: time.Now(), res: res}
	s.pricesMu.Unlock()
	writeJSON(w, http.StatusOK, res)
}

// fetchPrices asks Coinbase for every pair at once: the spot price, and the
// 24-hour stats where the pair has an order book.
func fetchPrices(ctx context.Context, pairs []string) map[string]*priceQuote {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	out := make(map[string]*priceQuote, len(pairs))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, pair := range pairs {
		q := &priceQuote{}
		out[pair] = q
		wg.Add(2)
		go func() {
			defer wg.Done()
			spot, err := fetchSpot(ctx, pair)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				q.Error = err.Error()
				return
			}
			q.Spot = spot
		}()
		go func() {
			defer wg.Done()
			st, err := fetchStats(ctx, pair)
			if err != nil {
				return // no order book, or a blip: the spot price still shows
			}
			mu.Lock()
			q.Open, q.High, q.Low, q.Volume = st.open, st.high, st.low, st.volume
			mu.Unlock()
		}()
	}
	wg.Wait()
	return out
}

func coinbaseGet(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "exe")
	res, err := pricesClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("coinbase: %w", err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	if err != nil {
		return nil, fmt.Errorf("coinbase: %w", err)
	}
	if res.StatusCode == http.StatusNotFound {
		return nil, errors.New("coinbase does not quote this pair")
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("coinbase answered %d", res.StatusCode)
	}
	return body, nil
}

func fetchSpot(ctx context.Context, pair string) (float64, error) {
	body, err := coinbaseGet(ctx, fmt.Sprintf(coinbaseSpotURL, pair))
	if err != nil {
		return 0, err
	}
	var v struct {
		Data struct {
			Amount string `json:"amount"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return 0, fmt.Errorf("coinbase: %w", err)
	}
	f, err := strconv.ParseFloat(v.Data.Amount, 64)
	if err != nil || f <= 0 {
		return 0, fmt.Errorf("coinbase: unreadable amount %q", v.Data.Amount)
	}
	return f, nil
}

type priceStats struct{ open, high, low, volume float64 }

func fetchStats(ctx context.Context, pair string) (priceStats, error) {
	body, err := coinbaseGet(ctx, fmt.Sprintf(coinbaseStatsURL, pair))
	if err != nil {
		return priceStats{}, err
	}
	var v struct {
		Open   string `json:"open"`
		High   string `json:"high"`
		Low    string `json:"low"`
		Volume string `json:"volume"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return priceStats{}, err
	}
	num := func(s string) float64 { f, _ := strconv.ParseFloat(s, 64); return f }
	st := priceStats{open: num(v.Open), high: num(v.High), low: num(v.Low), volume: num(v.Volume)}
	if st.open <= 0 {
		return priceStats{}, errors.New("no 24h open")
	}
	return st, nil
}
