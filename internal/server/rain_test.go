package server

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testRainState() *rainState { return &rainState{Episodes: map[string]*rainEpisode{}} }

func rainQuarters(now time.Time, vals ...[2]float64) []rainRow {
	// vals are {mm, prob} for consecutive quarters, the first one ending at
	// the next quarter mark after now; the hazard fields stay unknown
	end := now.Truncate(rainQuarter).Add(rainQuarter)
	rows := make([]rainRow, 0, len(vals))
	for i, v := range vals {
		rows = append(rows, rainRow{End: end.Add(time.Duration(i) * rainQuarter), MM: v[0], Prob: v[1],
			AppT: math.NaN(), RH: math.NaN(), Gust: math.NaN()})
	}
	return rows
}

// hazQuarters is five identical quarters with the SoCal fields set and no rain.
func hazQuarters(now time.Time, appT, rh, gust float64) []rainRow {
	end := now.Truncate(rainQuarter).Add(rainQuarter)
	rows := make([]rainRow, 0, 5)
	for i := 0; i < 5; i++ {
		rows = append(rows, rainRow{End: end.Add(time.Duration(i) * rainQuarter), Prob: -1,
			AppT: appT, RH: rh, Gust: gust})
	}
	return rows
}

func onlyRec(t *testing.T, recs []*rainRec) *rainRec {
	t.Helper()
	if len(recs) > 1 {
		t.Fatalf("more than one push in a tick: %+v, %+v", recs[0], recs[1])
	}
	if len(recs) == 0 {
		return nil
	}
	return recs[0]
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

// Rain's floor is joint, row by row: Berlin's 0.3 mm at 3% and its 0.0 mm
// at 55% both stay dry, Shanghai's 0.3 mm at 49% is wet, and a row without
// a probability leaves the millimetres to decide.
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

// The SoCal floors: heat on feels-like, wind on gusts, fire only when low
// humidity and high gusts arrive together — and a missing field never
// qualifies, at either the open or the keep line.
func TestHazardFloors(t *testing.T) {
	nan := math.NaN()
	for _, c := range []struct {
		h          string
		row        rainRow
		open, keep bool
	}{
		{"heat", rainRow{AppT: 41, RH: nan, Gust: nan}, true, true},
		{"heat", rainRow{AppT: 39, RH: nan, Gust: nan}, false, true},
		{"heat", rainRow{AppT: 37, RH: nan, Gust: nan}, false, false},
		{"heat", rainRow{AppT: nan, RH: nan, Gust: nan}, false, false},
		{"wind", rainRow{AppT: nan, RH: nan, Gust: 60}, true, true},
		{"wind", rainRow{AppT: nan, RH: nan, Gust: 50}, false, true},
		{"wind", rainRow{AppT: nan, RH: nan, Gust: 30}, false, false},
		{"fire", rainRow{AppT: nan, RH: 10, Gust: 60}, true, true},
		{"fire", rainRow{AppT: nan, RH: 10, Gust: 30}, false, false}, // dry but calm
		{"fire", rainRow{AppT: nan, RH: 40, Gust: 80}, false, false}, // windy but moist
		{"fire", rainRow{AppT: nan, RH: 18, Gust: 50}, false, true},  // between the lines
		{"fire", rainRow{AppT: nan, RH: nan, Gust: 80}, false, false},
	} {
		if got := hazardHit(c.h, c.row, false); got != c.open {
			t.Fatalf("%s open on %+v = %v, want %v", c.h, c.row, got, c.open)
		}
		if got := hazardHit(c.h, c.row, true); got != c.keep {
			t.Fatalf("%s keep on %+v = %v, want %v", c.h, c.row, got, c.keep)
		}
	}
}

// One rain episode, start to finish: rain opens it with the word the chance
// picks, a flicker above the keep floors holds it open in silence, only an
// all-dry forecast closes it and earns the dry push, and the cooldown holds
// a fresh shower from reopening at once.
func TestRainEpisode(t *testing.T) {
	st := testRainState()
	home := &rainPlace{ID: "geo:1", Name: "Los Angeles", CC: "US"}
	t0 := time.Date(2026, 9, 18, 8, 0, 0, 0, time.UTC)
	wet := [2]float64{0.3, 49}
	dry := [2]float64{0, 5}
	flicker := [2]float64{0.07, 30} // under the open floors, over the keep floors

	rec := onlyRec(t, rainStep(st, home, rainQuarters(t0, wet, dry, dry, dry, dry), t0))
	if rec == nil || rec.Hazard != "rain" || rec.Kind != "open" || rec.Word != "possible" ||
		!strings.Contains(rec.Title, "Rain possible soon in Los Angeles") {
		t.Fatalf("opening push: %+v", rec)
	}
	if st.Episodes["rain"].Place != home.ID {
		t.Fatalf("episode not open: %+v", st.Episodes["rain"])
	}
	t1 := t0.Add(rainPoll)
	if rec := onlyRec(t, rainStep(st, home, rainQuarters(t1, flicker, dry, dry, dry, dry), t1)); rec != nil {
		t.Fatalf("a flicker above the keep floors pushed: %+v", rec)
	}
	if st.Episodes["rain"].Place != home.ID {
		t.Fatal("the flicker closed the episode")
	}
	t2 := t1.Add(rainPoll)
	rec = onlyRec(t, rainStep(st, home, rainQuarters(t2, dry, dry, dry, dry, dry), t2))
	if rec == nil || rec.Kind != "clear" || !strings.Contains(rec.Title, "Next hour looks dry in Los Angeles") {
		t.Fatalf("closing push: %+v", rec)
	}
	if st.Episodes["rain"].Place != "" {
		t.Fatalf("episode still open: %+v", st.Episodes["rain"])
	}
	t3 := t2.Add(rainPoll) // five minutes on: inside the cooldown
	if rec := onlyRec(t, rainStep(st, home, rainQuarters(t3, wet, wet, dry, dry, dry), t3)); rec != nil {
		t.Fatalf("reopened inside the cooldown: %+v", rec)
	}
	t4 := t2.Add(rainCooldown) // the cooldown has passed
	rec = onlyRec(t, rainStep(st, home, rainQuarters(t4, [2]float64{0.4, 80}, wet, dry, dry, dry), t4))
	if rec == nil || rec.Word != "likely" || !strings.Contains(rec.Title, "Rain likely in Los Angeles in the next hour") {
		t.Fatalf("likely reopening: %+v", rec)
	}
}

// A Santa Ana day, tick by tick: dry wind opens fire weather alone — the
// plain wind push stays quiet under a red flag — the humidity's return
// clears fire and hands the still-blowing wind its own episode, and calm
// closes that too. US wording throughout: mph and the red-flag humidity.
func TestSoCalEpisodes(t *testing.T) {
	st := testRainState()
	home := &rainPlace{ID: "geo:1", Name: "Irvine", CC: "US"}
	t0 := time.Date(2026, 9, 18, 8, 0, 0, 0, time.UTC)

	rec := onlyRec(t, rainStep(st, home, hazQuarters(t0, 25, 10, 70), t0))
	if rec == nil || rec.Hazard != "fire" || rec.Kind != "open" ||
		!strings.Contains(rec.Title, "Fire weather in Irvine") ||
		!strings.Contains(rec.Body, "Gusts to 43 mph") || !strings.Contains(rec.Body, "humidity near 10%") {
		t.Fatalf("red-flag push: %+v", rec)
	}
	if e := st.Episodes["wind"]; e != nil && e.Place != "" {
		t.Fatalf("wind opened under a red flag: %+v", e)
	}
	t1 := t0.Add(rainPoll) // humidity recovers, the wind blows on
	recs := rainStep(st, home, hazQuarters(t1, 25, 40, 70), t1)
	if len(recs) != 2 || recs[0].Hazard != "fire" || recs[0].Kind != "clear" ||
		recs[1].Hazard != "wind" || recs[1].Kind != "open" ||
		!strings.Contains(recs[1].Title, "High wind in Irvine") {
		t.Fatalf("handoff from fire to wind: %+v", recs)
	}
	t2 := t1.Add(rainPoll) // calm
	rec = onlyRec(t, rainStep(st, home, hazQuarters(t2, 25, 40, 30), t2))
	if rec == nil || rec.Hazard != "wind" || rec.Kind != "clear" ||
		!strings.Contains(rec.Body, "back under 28 mph") {
		t.Fatalf("winds easing: %+v", rec)
	}
}

// Heat speaks the place's units: 41.5°C feels-like opens at 107°F for a US
// city and 42°C elsewhere, and the clear line names the keep floor.
func TestHeatEpisodeUnits(t *testing.T) {
	st := testRainState()
	us := &rainPlace{ID: "geo:1", Name: "Palm Springs", CC: "US"}
	t0 := time.Date(2026, 9, 18, 8, 0, 0, 0, time.UTC)
	rec := onlyRec(t, rainStep(st, us, hazQuarters(t0, 41.5, 30, 10), t0))
	if rec == nil || rec.Hazard != "heat" || !strings.Contains(rec.Title, "Dangerous heat in Palm Springs") ||
		!strings.Contains(rec.Body, "Feels like 107°F") {
		t.Fatalf("US heat push: %+v", rec)
	}
	t1 := t0.Add(rainPoll)
	rec = onlyRec(t, rainStep(st, us, hazQuarters(t1, 37, 30, 10), t1))
	if rec == nil || rec.Kind != "clear" || !strings.Contains(rec.Body, "back under 100°F") {
		t.Fatalf("US heat clear: %+v", rec)
	}

	st = testRainState()
	away := &rainPlace{ID: "geo:2", Name: "Seville", CC: "ES"}
	rec = onlyRec(t, rainStep(st, away, hazQuarters(t0, 41.5, 30, 10), t0))
	if rec == nil || !strings.Contains(rec.Body, "Feels like 42°C") {
		t.Fatalf("metric heat push: %+v", rec)
	}
}

// The day's cap, per hazard: the fifth opening in a sliding 24 hours is
// held back and noted once, the quiet holds while the forecast stays wet,
// and an opening aged past 24 hours frees its place.
func TestRainCap(t *testing.T) {
	t0 := time.Date(2026, 9, 18, 8, 0, 0, 0, time.UTC)
	st := testRainState()
	st.Episodes["rain"] = &rainEpisode{Opened: []time.Time{
		t0.Add(-23 * time.Hour), t0.Add(-18 * time.Hour), t0.Add(-12 * time.Hour), t0.Add(-2 * time.Hour),
	}}
	home := &rainPlace{ID: "geo:1", Name: "Los Angeles", CC: "US"}
	wet := [2]float64{0.3, 49}
	rec := onlyRec(t, rainStep(st, home, rainQuarters(t0, wet, wet, wet, wet, wet), t0))
	if rec == nil || !rec.HeldBack || st.Episodes["rain"].Place != "" {
		t.Fatalf("fifth opening not held back: rec %+v, state %+v", rec, st.Episodes["rain"])
	}
	t1 := t0.Add(rainPoll)
	if rec := onlyRec(t, rainStep(st, home, rainQuarters(t1, wet, wet, wet, wet, wet), t1)); rec != nil {
		t.Fatalf("a held-back episode noted twice: %+v", rec)
	}
	t2 := t0.Add(70 * time.Minute) // the 23-hours-ago opening has aged out
	rec = onlyRec(t, rainStep(st, home, rainQuarters(t2, wet, wet, wet, wet, wet), t2))
	if rec == nil || rec.HeldBack || st.Episodes["rain"].Place != home.ID {
		t.Fatalf("freed place not used: rec %+v, state %+v", rec, st.Episodes["rain"])
	}
}

// A new first city drops the open episode silently: no clear push for a
// place no longer home, and rain over the new city opens its own episode.
func TestRainPlaceChange(t *testing.T) {
	t0 := time.Date(2026, 9, 18, 8, 0, 0, 0, time.UTC)
	st := testRainState()
	st.Episodes["rain"] = &rainEpisode{Place: "geo:1", City: "Los Angeles", OpenedAt: t0.Add(-time.Hour)}
	there := &rainPlace{ID: "geo:2", Name: "Irvine", CC: "US"}
	dry := [2]float64{0, 5}
	if rec := onlyRec(t, rainStep(st, there, rainQuarters(t0, dry, dry, dry, dry, dry), t0)); rec != nil {
		t.Fatalf("clear push for the old city: %+v", rec)
	}
	if st.Episodes["rain"].Place != "" {
		t.Fatalf("old episode survived the move: %+v", st.Episodes["rain"])
	}
	st = testRainState()
	st.Episodes["rain"] = &rainEpisode{Place: "geo:1", City: "Los Angeles", OpenedAt: t0.Add(-time.Hour)}
	wet := [2]float64{0.3, 49}
	rec := onlyRec(t, rainStep(st, there, rainQuarters(t0, wet, dry, dry, dry, dry), t0))
	if rec == nil || rec.Kind != "open" || rec.City != "Irvine" || st.Episodes["rain"].Place != "geo:2" {
		t.Fatalf("new city's own episode: rec %+v, state %+v", rec, st.Episodes["rain"])
	}
}

// The first build's state file — the rain episode at the top level — loads
// into Episodes["rain"], and a current file round-trips.
func TestRainStateMigration(t *testing.T) {
	s := &Server{StateDir: t.TempDir()}
	t0 := time.Date(2026, 9, 18, 8, 0, 0, 0, time.UTC)
	legacy := map[string]any{
		"place": "geo:1", "city": "Los Angeles",
		"opened_at": t0, "opened": []time.Time{t0}, "last_ok": t0,
	}
	b, _ := json.Marshal(legacy)
	if err := os.WriteFile(s.rainStatePath(), b, 0o600); err != nil {
		t.Fatal(err)
	}
	st := s.loadRainState()
	e := st.Episodes["rain"]
	if e == nil || e.Place != "geo:1" || e.City != "Los Angeles" || len(e.Opened) != 1 || !st.LastOK.Equal(t0) {
		t.Fatalf("migrated state: %+v, episode %+v", st, e)
	}
	s.saveRainState(st)
	again := s.loadRainState()
	if again.Episodes["rain"] == nil || again.Episodes["rain"].Place != "geo:1" {
		t.Fatalf("round trip lost the episode: %+v", again)
	}
}

// The first row is the app's own order: the fractional drag rank where one
// is set, created where none is, deleted rows out of the running — and the
// country code rides along for the units.
func TestRainTopPlace(t *testing.T) {
	s := &Server{StateDir: t.TempDir()}
	dir := s.appDataDir("Weather")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	rank := 5.0
	doc := map[string]any{"version": 1, "items": []map[string]any{
		{"id": "geo:1", "name": "First by created", "lat": 1.0, "lon": 1.0, "created": 10.0},
		{"id": "geo:2", "name": "Ranked ahead", "cc": "US", "lat": 2.0, "lon": 2.0, "created": 20.0, "order": rank},
		{"id": "geo:3", "name": "Deleted", "lat": 3.0, "lon": 3.0, "created": 1.0, "deleted": 99},
	}}
	b, _ := json.Marshal(doc)
	if err := os.WriteFile(filepath.Join(dir, "places.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	p := s.rainTopPlace()
	if p == nil || p.ID != "geo:2" || p.CC != "US" {
		t.Fatalf("top place %+v, want the ranked geo:2 with its cc", p)
	}
}
