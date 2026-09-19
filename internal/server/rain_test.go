package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func rainQuarters(now time.Time, vals ...[2]float64) []rainRow {
	// vals are {mm, prob} for consecutive quarters, the first one ending at
	// the next quarter mark after now
	end := now.Truncate(rainQuarter).Add(rainQuarter)
	rows := make([]rainRow, 0, len(vals))
	for i, v := range vals {
		rows = append(rows, rainRow{End: end.Add(time.Duration(i) * rainQuarter), MM: v[0], Prob: v[1]})
	}
	return rows
}

// The window is overlap with the next 60 minutes, stamps read as interval
// ends and edge quarters taken whole: at 14:11 the rows stamped 14:15
// through 15:15 belong in, the ended 14:00 and the wholly-later 15:30 do
// not — the thread's own worked example.
func TestRainWindowRows(t *testing.T) {
	now := time.Date(2026, 9, 18, 14, 11, 0, 0, time.UTC)
	var rows []rainRow
	for m := 0; m <= 90; m += 15 { // 14:00 through 15:30
		rows = append(rows, rainRow{End: time.Date(2026, 9, 18, 14, 0, 0, 0, time.UTC).Add(time.Duration(m) * time.Minute)})
	}
	win := rainWindowRows(rows, now)
	if len(win) != 5 {
		t.Fatalf("window holds %d quarters, want 5", len(win))
	}
	if !win[0].End.Equal(time.Date(2026, 9, 18, 14, 15, 0, 0, time.UTC)) ||
		!win[4].End.Equal(time.Date(2026, 9, 18, 15, 15, 0, 0, time.UTC)) {
		t.Fatalf("window runs %v to %v, want 14:15 to 15:15", win[0].End, win[4].End)
	}
}

// The floor is joint, row by row: Berlin's 0.3 mm at 3% and its 0.0 mm at
// 55% both stay dry, Shanghai's 0.3 mm at 49% is wet, and a row without a
// probability leaves the millimetres to decide.
func TestRainJointFloor(t *testing.T) {
	for _, c := range []struct {
		row rainRow
		wet bool
	}{
		{rainRow{MM: 0.3, Prob: 3}, false},
		{rainRow{MM: 0, Prob: 55}, false},
		{rainRow{MM: 0.3, Prob: 49}, true},
		{rainRow{MM: 0.2, Prob: -1}, true},
		{rainRow{MM: 0.05, Prob: 80}, false},
	} {
		if got := rainWet(c.row, rainOpenMM, rainOpenProb); got != c.wet {
			t.Fatalf("%.2f mm at %.0f%%: wet = %v, want %v", c.row.MM, c.row.Prob, got, c.wet)
		}
	}
}

// One episode, start to finish: rain opens it with the word the chance
// picks, a flicker above the keep floors holds it open in silence, only an
// all-dry forecast closes it and earns the dry push, and the cooldown holds
// a fresh shower from reopening at once.
func TestRainEpisode(t *testing.T) {
	st := &rainState{}
	home := &rainPlace{ID: "geo:1", Name: "Los Angeles"}
	t0 := time.Date(2026, 9, 18, 8, 0, 0, 0, time.UTC)
	wet := [2]float64{0.3, 49}
	dry := [2]float64{0, 5}
	flicker := [2]float64{0.07, 30} // under the open floors, over the keep floors

	rec := rainStep(st, home, rainQuarters(t0, wet, dry, dry, dry, dry), t0)
	if rec == nil || rec.Kind != "rain" || rec.Word != "possible" || !strings.Contains(rec.Title, "Rain possible soon in Los Angeles") {
		t.Fatalf("opening push: %+v", rec)
	}
	if st.Place != home.ID {
		t.Fatalf("episode not open: %+v", st)
	}
	t1 := t0.Add(rainPoll)
	if rec := rainStep(st, home, rainQuarters(t1, flicker, dry, dry, dry, dry), t1); rec != nil {
		t.Fatalf("a flicker above the keep floors pushed: %+v", rec)
	}
	if st.Place != home.ID {
		t.Fatal("the flicker closed the episode")
	}
	t2 := t1.Add(rainPoll)
	rec = rainStep(st, home, rainQuarters(t2, dry, dry, dry, dry, dry), t2)
	if rec == nil || rec.Kind != "dry" || !strings.Contains(rec.Title, "Next hour looks dry in Los Angeles") {
		t.Fatalf("closing push: %+v", rec)
	}
	if st.Place != "" {
		t.Fatalf("episode still open: %+v", st)
	}
	t3 := t2.Add(rainPoll) // five minutes on: inside the cooldown
	if rec := rainStep(st, home, rainQuarters(t3, wet, wet, dry, dry, dry), t3); rec != nil {
		t.Fatalf("reopened inside the cooldown: %+v", rec)
	}
	t4 := t2.Add(rainCooldown) // the cooldown has passed
	rec = rainStep(st, home, rainQuarters(t4, [2]float64{0.4, 80}, wet, dry, dry, dry), t4)
	if rec == nil || rec.Word != "likely" || !strings.Contains(rec.Title, "Rain likely in Los Angeles in the next hour") {
		t.Fatalf("likely reopening: %+v", rec)
	}
}

// The day's cap: the fifth opening in a sliding 24 hours is held back and
// noted once, the quiet holds while the forecast stays wet, and an opening
// aged past 24 hours frees its place.
func TestRainCap(t *testing.T) {
	t0 := time.Date(2026, 9, 18, 8, 0, 0, 0, time.UTC)
	st := &rainState{Opened: []time.Time{
		t0.Add(-23 * time.Hour), t0.Add(-18 * time.Hour), t0.Add(-12 * time.Hour), t0.Add(-2 * time.Hour),
	}}
	home := &rainPlace{ID: "geo:1", Name: "Los Angeles"}
	wet := [2]float64{0.3, 49}
	rec := rainStep(st, home, rainQuarters(t0, wet, wet, wet, wet, wet), t0)
	if rec == nil || !rec.HeldBack || st.Place != "" {
		t.Fatalf("fifth opening not held back: rec %+v, state %+v", rec, st)
	}
	t1 := t0.Add(rainPoll)
	if rec := rainStep(st, home, rainQuarters(t1, wet, wet, wet, wet, wet), t1); rec != nil {
		t.Fatalf("a held-back episode noted twice: %+v", rec)
	}
	t2 := t0.Add(70 * time.Minute) // the 23-hours-ago opening has aged out
	rec = rainStep(st, home, rainQuarters(t2, wet, wet, wet, wet, wet), t2)
	if rec == nil || rec.HeldBack || st.Place != home.ID {
		t.Fatalf("freed place not used: rec %+v, state %+v", rec, st)
	}
}

// A new first city drops the open episode silently: no dry push for a place
// no longer home, and rain over the new city opens its own episode.
func TestRainPlaceChange(t *testing.T) {
	t0 := time.Date(2026, 9, 18, 8, 0, 0, 0, time.UTC)
	st := &rainState{Place: "geo:1", City: "Los Angeles", OpenedAt: t0.Add(-time.Hour)}
	there := &rainPlace{ID: "geo:2", Name: "Irvine"}
	dry := [2]float64{0, 5}
	if rec := rainStep(st, there, rainQuarters(t0, dry, dry, dry, dry, dry), t0); rec != nil {
		t.Fatalf("dry push for the old city: %+v", rec)
	}
	if st.Place != "" {
		t.Fatalf("old episode survived the move: %+v", st)
	}
	st = &rainState{Place: "geo:1", City: "Los Angeles", OpenedAt: t0.Add(-time.Hour)}
	wet := [2]float64{0.3, 49}
	rec := rainStep(st, there, rainQuarters(t0, wet, dry, dry, dry, dry), t0)
	if rec == nil || rec.Kind != "rain" || rec.City != "Irvine" || st.Place != "geo:2" {
		t.Fatalf("new city's own episode: rec %+v, state %+v", rec, st)
	}
}

// The first row is the app's own order: the fractional drag rank where one
// is set, created where none is, deleted rows out of the running.
func TestRainTopPlace(t *testing.T) {
	s := &Server{StateDir: t.TempDir()}
	dir := s.appDataDir("Weather")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	rank := 5.0
	doc := map[string]any{"version": 1, "items": []map[string]any{
		{"id": "geo:1", "name": "First by created", "lat": 1.0, "lon": 1.0, "created": 10.0},
		{"id": "geo:2", "name": "Ranked ahead", "lat": 2.0, "lon": 2.0, "created": 20.0, "order": rank},
		{"id": "geo:3", "name": "Deleted", "lat": 3.0, "lon": 3.0, "created": 1.0, "deleted": 99},
	}}
	b, _ := json.Marshal(doc)
	if err := os.WriteFile(filepath.Join(dir, "places.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	p := s.rainTopPlace()
	if p == nil || p.ID != "geo:2" {
		t.Fatalf("top place %+v, want the ranked geo:2", p)
	}
}
