package server

import (
	"strings"
	"testing"
	"time"
)

// The rule, minute by minute on a clock: a hit must hold two minutes, a
// token re-arms only a full hour-threshold away from its last alert, thirty
// minutes of quiet between alerts, the day's later alerts need bigger moves,
// the fifth in 24 hours is held back and rides along with the next one.
func TestAlertRule(t *testing.T) {
	st := &alertState{Samples: map[string][]priceSample{}, PrevHit: map[string]bool{}, NextID: 1}
	t0 := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	feed := func(minute int, p float64) *alertRec {
		now := t0.Add(time.Duration(minute) * time.Minute)
		st.Samples["SOL"] = appendSample(st.Samples["SOL"], priceSample{T: now.Unix(), P: p}, now)
		return alertStep(st, "SOL", now)
	}
	quiet := func(from, to int, p float64) {
		for m := from; m <= to; m++ {
			if r := feed(m, p); r != nil {
				t.Fatalf("minute %d at %v: unexpected alert %+v", m, p, *r)
			}
		}
	}
	quiet(0, 89, 100)
	// +2.6% against an hour ago: the first minute is only a hit, the second alerts
	if r := feed(90, 102.6); r != nil {
		t.Fatalf("fired on the first minute of a hit: %+v", *r)
	}
	r := feed(91, 102.6)
	if r == nil || r.Window != "1h" || r.K != 1 || r.From != 100 || r.Change < 2.5 {
		t.Fatalf("first alert: %+v", r)
	}
	// holding there is not news: the ladder wants a full 2.5% from 102.6
	quiet(92, 123, 102.7)
	// a second leg to 105.3 is 2.6% up the ladder, and +5.3% on the hour beats the 1.5x threshold
	feed(124, 105.3)
	r = feed(125, 105.3)
	if r == nil || r.K != 2 {
		t.Fatalf("second alert: %+v", r)
	}
	// straight on to 108.5 qualifies on size but not on time: 30 minutes of quiet first
	quiet(126, 154, 108.5)
	r = feed(155, 108.5)
	if r == nil || r.K != 3 {
		t.Fatalf("third alert after the cooldown: %+v", r)
	}
	quiet(156, 185, 108.5)
	feed(186, 115)
	r = feed(187, 115)
	if r == nil || r.K != 4 {
		t.Fatalf("fourth alert: %+v", r)
	}
	// the fifth move of the day is held back (the hit was already holding at 115, so it lands at once)
	quiet(188, 217, 115)
	r = feed(218, 122)
	if r == nil || !r.HeldBack {
		t.Fatalf("fifth move should be held back: %+v", r)
	}
	// a flat day passes with nothing more (the 24h window is up +22% but the ladder holds)
	quiet(219, 1598, 122)
	// three alerts aged out: the next one is allowed, and carries the held-back one
	r = feed(1599, 130)
	if r == nil || r.HeldBack || r.Unreported != 1 || r.K != 2 || r.Window != "1h" {
		t.Fatalf("after the window freed: %+v", r)
	}
	title, body := alertText(r, t0.Add(1599*time.Minute))
	if title != "SOL +6.6% in the last hour" {
		t.Errorf("title %q", title)
	}
	for _, want := range []string{"$130.00, from $122.00", "2nd of 4 today", "1 more move went unreported"} {
		if !strings.Contains(body, want) {
			t.Errorf("body %q lacks %q", body, want)
		}
	}
	// nothing fires without a token's own rule
	st.Samples["XYZ"] = []priceSample{{T: t0.Unix(), P: 1}}
	if alertStep(st, "XYZ", t0) != nil {
		t.Error("unknown token alerted")
	}
}

// An ecosystem token's text carries its price in SOL and the move in SOL terms.
func TestAlertTextEcosystem(t *testing.T) {
	rec := &alertRec{Token: "PUMP", Window: "1h", Change: 10.28, Price: 0.004688, From: 0.004251, Day: 21.14, InSOL: 0.0000493, SOLChange: 8.1, K: 1}
	title, body := alertText(rec, time.Now())
	if title != "PUMP +10.3% in the last hour" {
		t.Errorf("title %q", title)
	}
	if body != "$0.004688 = 0.00004930 SOL (+8.1% in SOL) · 24h +21.1% · 1st of 4 today" {
		t.Errorf("body %q", body)
	}
	held := &alertRec{Token: "SKR", Window: "24h", Change: -31, Price: 0.0206, From: 0.0299, HeldBack: true}
	_, body = alertText(held, time.Now())
	if !strings.Contains(body, "held back") || strings.Contains(body, "of 4 today") {
		t.Errorf("held-back body %q", body)
	}
}

func TestAlertNumberFormats(t *testing.T) {
	for v, want := range map[float64]string{1234.5: "$1,234.50", 97.1: "$97.10", 0.19655: "$0.1966", 0.003506: "$0.003506", 0.0000361: "$0.00003610"} {
		if got := fmtUSD(v); got != want {
			t.Errorf("fmtUSD(%v) = %q, want %q", v, got, want)
		}
	}
	if fmtPct(-3.75) != "−3.8%" || fmtPct(0.04) != "+0.0%" {
		t.Errorf("fmtPct: %q %q", fmtPct(-3.75), fmtPct(0.04))
	}
}

// A sample only counts as "an hour ago" within the tolerance.
func TestSampleNear(t *testing.T) {
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	xs := []priceSample{{T: now.Add(-70 * time.Minute).Unix(), P: 1}, {T: now.Add(-62 * time.Minute).Unix(), P: 2}, {T: now.Add(-50 * time.Minute).Unix(), P: 3}}
	if s := sampleNear(xs, now.Add(-time.Hour)); s == nil || s.P != 2 {
		t.Fatalf("nearest: %+v", s)
	}
	if s := sampleNear(xs[:1], now.Add(-time.Hour)); s != nil {
		t.Fatalf("70 minutes ago is not an hour ago: %+v", s)
	}
	if s := sampleNear(nil, now); s != nil {
		t.Fatal("empty")
	}
}
