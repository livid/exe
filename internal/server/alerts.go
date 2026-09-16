package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Price alerts for the Control Strip ticker — the rule of docs/price-alerts.md.
// A sampler takes Coinbase's spot prices every minute whether or not a desktop
// is open (and fills the /v1/prices cache on the way, so the tiles' own polls
// cost nothing), keeps a day of samples per token, and sends a notification
// when a token moves more in an hour or a day than it rarely does — at most
// four per token in any sliding 24 hours.

// alertRule is a token's thresholds: the percent move within one hour, and
// within 24 hours, that earns a notification. Set at about the top
// half-percent of the token's hours and top five percent of its days over
// a trailing month (docs/price-alerts-calibrate.py), then eased a little.
type alertRule struct{ Hour, Day float64 }

var alertRules = map[string]alertRule{
	"SOL":  {2.5, 6},
	"PUMP": {5, 12},
	"MET":  {6, 15},
	"SKR":  {8, 25},
}

// alertTokens is the order the ticker shows them; alertPairs is the ticker's
// exact pair list, so the sampler's answer is the one the tile asks for.
var alertTokens = []string{"SOL", "PUMP", "MET", "SKR"}

const alertPairs = "SOL-USD,PUMP-USD,MET-USD,SKR-USD,PUMP-SOL,MET-SOL,SKR-SOL"

const (
	alertCap       = 4                // per token per sliding 24h
	alertCooldown  = 30 * time.Minute // between a token's alerts, held-back ones included
	alertKeep      = 25 * time.Hour   // samples kept per token
	alertRefTol    = 3 * time.Minute  // how close a reference sample must be to "an hour ago"
	alertStateFile = "alerts-state.json"
	alertLogFile   = "alerts.jsonl"
)

// alertMults escalates the day's later alerts: the k-th alert within 24h
// needs this multiple of the threshold, so a wild day spends its four on
// progressively bigger news.
var alertMults = [alertCap]float64{1, 1.5, 2, 2}

type priceSample struct {
	T int64   `json:"t"` // unix seconds
	P float64 `json:"p"`
}

// alertRec is one move that qualified — delivered, or held back by the cap.
type alertRec struct {
	ID         int64     `json:"id"`
	T          time.Time `json:"t"`
	Token      string    `json:"token"`
	Window     string    `json:"window"` // "1h" or "24h"
	Change     float64   `json:"change"` // percent over that window
	Price      float64   `json:"price"`
	From       float64   `json:"from"`
	Day        float64   `json:"day"`                  // percent over 24h, for the text
	InSOL      float64   `json:"in_sol,omitempty"`     // price in SOL (ecosystem tokens)
	SOLChange  float64   `json:"sol_change,omitempty"` // percent change in SOL terms over the window
	K          int       `json:"k"`                    // 1..4, its place in the day
	HeldBack   bool      `json:"held_back,omitempty"`  // the cap was spent
	Unreported int       `json:"unreported,omitempty"` // held-back moves folded into this one
	Title      string    `json:"title"`
	Body       string    `json:"body"`
	Pushed     int       `json:"pushed,omitempty"`
}

type alertState struct {
	Samples map[string][]priceSample `json:"samples"`
	Alerts  []alertRec               `json:"alerts"`
	NextID  int64                    `json:"next_id"`
	PrevHit map[string]bool          `json:"prev_hit"`
	Started time.Time                `json:"started"`
	LastOK  time.Time                `json:"last_ok"`
	LastErr string                   `json:"last_err,omitempty"`
}

func (s *Server) alertStatePath() string { return filepath.Join(s.StateDir, alertStateFile) }

func (s *Server) loadAlertState() *alertState {
	st := &alertState{Samples: map[string][]priceSample{}, PrevHit: map[string]bool{}, NextID: 1, Started: time.Now().UTC()}
	if b, err := os.ReadFile(s.alertStatePath()); err == nil {
		var saved alertState
		if json.Unmarshal(b, &saved) == nil && saved.Samples != nil {
			saved.Started = st.Started
			if saved.PrevHit == nil {
				saved.PrevHit = map[string]bool{}
			}
			if saved.NextID == 0 {
				saved.NextID = 1
			}
			return &saved
		}
	}
	return st
}

func (s *Server) saveAlertState(st *alertState) {
	b, err := json.Marshal(st)
	if err != nil {
		return
	}
	tmp := s.alertStatePath() + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, s.alertStatePath())
	}
}

// RunAlerts is the sampler: one Coinbase round per minute, then the rule for
// each token. It runs for the daemon's life.
func (s *Server) RunAlerts(ctx context.Context) {
	st := s.loadAlertState()
	s.alertMu.Lock()
	s.alerts = st
	s.alertMu.Unlock()
	log.Printf("price alerts: watching %s (%d samples on file, %d alerts)", strings.Join(alertTokens, ", "), countSamples(st), len(st.Alerts))
	for ctx.Err() == nil {
		s.alertTick(ctx, time.Now())
		// wake just past the next whole minute so samples land on a steady grid
		wait := time.Until(time.Now().Truncate(time.Minute).Add(time.Minute + 2*time.Second))
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

func countSamples(st *alertState) int {
	n := 0
	for _, v := range st.Samples {
		n += len(v)
	}
	return n
}

// alertTick fetches, records, evaluates, delivers and saves.
func (s *Server) alertTick(ctx context.Context, now time.Time) {
	pairs, _ := parsePricePairs(alertPairs)
	quotes := fetchPrices(ctx, pairs)
	res := map[string]any{"checked_at": now.UTC(), "prices": quotes}
	s.pricesMu.Lock()
	if s.prices == nil {
		s.prices = map[string]pricesEntry{}
	}
	s.prices[alertPairs] = pricesEntry{at: now, res: res}
	s.pricesMu.Unlock()

	s.alertMu.Lock()
	defer s.alertMu.Unlock()
	st := s.alerts
	if st == nil {
		return
	}
	ok := 0
	for _, tok := range alertTokens {
		q := quotes[tok+"-USD"]
		if q == nil || q.Spot <= 0 {
			continue
		}
		ok++
		st.Samples[tok] = appendSample(st.Samples[tok], priceSample{T: now.Unix(), P: q.Spot}, now)
	}
	if ok == 0 {
		st.LastErr = "Coinbase did not answer"
		if q := quotes["SOL-USD"]; q != nil && q.Error != "" {
			st.LastErr = q.Error
		}
		s.saveAlertState(st)
		return
	}
	st.LastOK, st.LastErr = now.UTC(), ""
	var fresh []*alertRec
	for _, tok := range alertTokens {
		if quotes[tok+"-USD"] == nil || quotes[tok+"-USD"].Spot <= 0 {
			continue
		}
		if rec := alertStep(st, tok, now); rec != nil {
			if k := quotes[tok+"-SOL"]; k != nil && k.Spot > 0 {
				rec.InSOL = k.Spot
			}
			rec.Title, rec.Body = alertText(rec, now)
			fresh = append(fresh, rec)
		}
	}
	// keep two days of alerts on file; the log keeps them all
	cut := now.Add(-48 * time.Hour)
	kept := st.Alerts[:0]
	for _, a := range st.Alerts {
		if a.T.After(cut) {
			kept = append(kept, a)
		}
	}
	st.Alerts = kept
	s.saveAlertState(st)
	for _, rec := range fresh {
		s.appendAlertLog(rec)
		if rec.HeldBack {
			log.Printf("price alerts: %s %s (held back, %d of the day's %d used)", rec.Token, rec.Title, alertCap, alertCap)
			continue
		}
		log.Printf("price alerts: %s — %s", rec.Title, rec.Body)
		r := rec
		go func() {
			n, errs := s.pushAll(context.Background(), pushMessage{Title: r.Title, Body: r.Body, Tag: "px-" + r.Token, URL: "/"})
			for _, e := range errs {
				log.Printf("price alerts: push: %s", e)
			}
			s.alertMu.Lock()
			for i := range s.alerts.Alerts {
				if s.alerts.Alerts[i].ID == r.ID {
					s.alerts.Alerts[i].Pushed = n
				}
			}
			s.alertMu.Unlock()
		}()
	}
}

func appendSample(xs []priceSample, x priceSample, now time.Time) []priceSample {
	xs = append(xs, x)
	cut := now.Add(-alertKeep).Unix()
	i := 0
	for i < len(xs) && xs[i].T < cut {
		i++
	}
	return xs[i:]
}

// sampleNear is the sample closest to t, if one lies within alertRefTol.
func sampleNear(xs []priceSample, t time.Time) *priceSample {
	want := t.Unix()
	i := sort.Search(len(xs), func(i int) bool { return xs[i].T >= want })
	var best *priceSample
	for _, j := range []int{i - 1, i} {
		if j < 0 || j >= len(xs) {
			continue
		}
		if best == nil || abs64(xs[j].T-want) < abs64(best.T-want) {
			best = &xs[j]
		}
	}
	if best == nil || abs64(best.T-want) > int64(alertRefTol/time.Second) {
		return nil
	}
	return best
}

func abs64(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

func pctChange(now, then float64) float64 { return (now/then - 1) * 100 }

// alertStep applies the rule to one token at one minute, on the samples
// already recorded, and returns the move if it qualifies (delivered or held
// back), appending it to the state. Pure on the state, so the tests can
// drive it with a clock.
func alertStep(st *alertState, tok string, now time.Time) *alertRec {
	rule, ok := alertRules[tok]
	xs := st.Samples[tok]
	if !ok || len(xs) == 0 || xs[len(xs)-1].T != now.Unix() {
		return nil
	}
	p := xs[len(xs)-1].P
	ref1 := sampleNear(xs, now.Add(-time.Hour))
	ref24 := sampleNear(xs, now.Add(-24*time.Hour))

	var recent []alertRec // delivered within the sliding day
	var last *alertRec    // the newest, delivered or held back
	for i := range st.Alerts {
		a := &st.Alerts[i]
		if a.Token != tok {
			continue
		}
		if last == nil || a.T.After(last.T) {
			last = a
		}
		if !a.HeldBack && now.Sub(a.T) < 24*time.Hour {
			recent = append(recent, *a)
		}
	}
	m := alertMults[min(len(recent), alertCap-1)]
	r1, r24 := 0.0, 0.0
	hit1, hit24 := false, false
	if ref1 != nil {
		r1 = pctChange(p, ref1.P)
		hit1 = math.Abs(r1) >= m*rule.Hour
	}
	if ref24 != nil {
		r24 = pctChange(p, ref24.P)
		hit24 = math.Abs(r24) >= m*rule.Day
	}
	hit := hit1 || hit24
	held := st.PrevHit[tok]
	st.PrevHit[tok] = hit
	if !hit || !held { // a hit must hold on two consecutive minutes
		return nil
	}
	if last != nil {
		if now.Sub(last.T) < alertCooldown {
			return nil
		}
		// re-arm: a full hour-threshold away from the last alert's price
		if now.Sub(last.T) < 24*time.Hour && math.Abs(pctChange(p, last.Price)) < rule.Hour {
			return nil
		}
	}
	rec := alertRec{ID: st.NextID, T: now.UTC(), Token: tok, Price: p, Day: r24, K: len(recent) + 1}
	st.NextID++
	if hit1 {
		rec.Window, rec.Change, rec.From = "1h", r1, ref1.P
	} else {
		rec.Window, rec.Change, rec.From = "24h", r24, ref24.P
	}
	if tok != "SOL" { // the same move in SOL terms, from SOL's own samples
		if sol := st.Samples["SOL"]; len(sol) > 0 && sol[len(sol)-1].T == now.Unix() {
			ref := time.Hour
			if rec.Window == "24h" {
				ref = 24 * time.Hour
			}
			if s0 := sampleNear(sol, now.Add(-ref)); s0 != nil {
				rec.SOLChange = ((1+rec.Change/100)/(1+pctChange(sol[len(sol)-1].P, s0.P)/100) - 1) * 100
			}
		}
	}
	if len(recent) >= alertCap {
		rec.HeldBack = true
	} else {
		// held-back moves since the last delivered alert ride along
		for _, a := range st.Alerts {
			if a.Token == tok && a.HeldBack && now.Sub(a.T) < 24*time.Hour && (len(recent) == 0 || a.T.After(recent[len(recent)-1].T)) {
				rec.Unreported++
			}
		}
	}
	st.Alerts = append(st.Alerts, rec)
	return &st.Alerts[len(st.Alerts)-1]
}

// alertText is the notification, in the ticker's own number formats:
//
//	SOL −8.4% in the last hour
//	$93.58, from $102.20 · 24h +3.9% · 3rd of 4 today
func alertText(rec *alertRec, now time.Time) (string, string) {
	win := "in the last hour"
	if rec.Window == "24h" {
		win = "in 24 hours"
	}
	title := fmt.Sprintf("%s %s %s", rec.Token, fmtPct(rec.Change), win)
	parts := []string{}
	if rec.Token == "SOL" || rec.InSOL == 0 {
		parts = append(parts, fmt.Sprintf("%s, from %s", fmtUSD(rec.Price), fmtUSD(rec.From)))
	} else {
		p := fmt.Sprintf("%s = %s SOL", fmtUSD(rec.Price), fmtSig(rec.InSOL))
		if rec.SOLChange != 0 {
			p += fmt.Sprintf(" (%s in SOL)", fmtPct(rec.SOLChange))
		}
		parts = append(parts, p)
	}
	if rec.Window == "1h" && rec.Day != 0 {
		parts = append(parts, "24h "+fmtPct(rec.Day))
	}
	if rec.HeldBack {
		parts = append(parts, "held back: the day's 4 alerts are spent")
	} else {
		parts = append(parts, fmt.Sprintf("%s of %d today", ordinal(rec.K), alertCap))
	}
	if rec.Unreported > 0 {
		parts = append(parts, fmt.Sprintf("%d more move%s went unreported", rec.Unreported, plural(rec.Unreported)))
	}
	return title, strings.Join(parts, " · ")
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func ordinal(k int) string {
	switch k {
	case 1:
		return "1st"
	case 2:
		return "2nd"
	case 3:
		return "3rd"
	}
	return strconv.Itoa(k) + "th"
}

// fmtPct writes +3.2% / −3.2% with the true minus sign the tile uses.
func fmtPct(v float64) string {
	sign := "+"
	if v < 0 {
		sign = "−"
	}
	return fmt.Sprintf("%s%.1f%%", sign, math.Abs(v))
}

// fmtUSD mirrors the tile: cents above a dollar, four significant figures below.
func fmtUSD(v float64) string {
	if v >= 1 {
		s := strconv.FormatFloat(v, 'f', 2, 64)
		dot := strings.IndexByte(s, '.')
		whole := s[:dot]
		for i := len(whole) - 3; i > 0; i -= 3 {
			whole = whole[:i] + "," + whole[i:]
		}
		return "$" + whole + s[dot:]
	}
	return "$" + fmtSig(v)
}

// fmtSig is four significant figures in plain decimals (never exponent form).
func fmtSig(v float64) string {
	if v <= 0 {
		return "0"
	}
	decimals := 3 - int(math.Floor(math.Log10(v)))
	if decimals < 0 {
		decimals = 0
	}
	return strconv.FormatFloat(v, 'f', decimals, 64)
}

func (s *Server) appendAlertLog(rec *alertRec) {
	f, err := os.OpenFile(filepath.Join(s.StateDir, alertLogFile), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	b, _ := json.Marshal(rec)
	f.Write(append(b, '\n'))
}

// GET /v1/alerts — the recent moves (newest first) and the sampler's state,
// for the ticker's menu and its toasts.
func (s *Server) handleAlerts(w http.ResponseWriter, r *http.Request) {
	s.alertMu.Lock()
	st := s.alerts
	var out []alertRec
	var status map[string]any
	if st != nil {
		for i := len(st.Alerts) - 1; i >= 0 && len(out) < 20; i-- {
			out = append(out, st.Alerts[i])
		}
		status = map[string]any{"started": st.Started, "last_ok": st.LastOK, "last_err": st.LastErr, "samples": countSamples(st)}
	}
	s.alertMu.Unlock()
	if out == nil {
		out = []alertRec{}
	}
	rules := map[string]any{}
	for tok, r := range alertRules {
		rules[tok] = map[string]float64{"hour": r.Hour, "day": r.Day}
	}
	writeJSON(w, http.StatusOK, map[string]any{"alerts": out, "rules": rules, "cap": alertCap, "status": status})
}
