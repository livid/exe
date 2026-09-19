package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

// Weather alerts for the Weather app's first city. A sampler polls
// Open-Meteo's quarter-hour rows for the top place in Weather's list — the
// same row the app draws first, the drag rank deciding — and sends a Web
// Push when a hazard reaches the next 60 minutes, and one more when a
// fresh forecast clears it. The rule, shaped in the hub thread that asked
// for it: the quarter rows, not the hourly ones (a stamp is its interval's
// end, so the first hour row is the hour already over); the window test is
// overlap, edge quarters taken whole; a quarter qualifies only over its
// floors, row by row. A failed poll or a forecast that comes back short
// writes nothing: silence never counts as clear, and only a fresh all-calm
// forecast closes an open episode.
//
// Four hazards ride the one call, each an episode of its own on the same
// machinery. Rain is the joint millimetres-and-probability floor, the
// chance picking the word. The other three are the SoCal set Livid asked
// for, their floors the NWS's own criteria, not taste: heat opens at a
// feels-like of 105°F (the LA-county heat-advisory line), wind at gusts of
// 35 mph (the Wind Advisory line), and fire weather is the Red Flag
// pairing — humidity at or under 15% while gusts reach that same line —
// which neither field earns alone. A red-flag hour outranks the plain wind
// push, so one Santa Ana event taps once. Pushes speak °F and mph when the
// first city sits in the US, °C and km/h otherwise.

const (
	rainPoll      = 5 * time.Minute
	rainWindow    = time.Hour
	rainQuarter   = 15 * time.Minute
	rainStateFile = "rain-state.json"
	rainLogFile   = "rain.jsonl"

	// A quarter qualifies at the open floors; an open episode stays open
	// down to the keep floors, so a reading flickering around the open
	// line does not tap twice. Rain's probability rows are hourly-grade
	// however fine the quarters look (drawn as a line between the hours),
	// so its floors are set for that, and a row without a probability
	// passes on millimetres alone.
	rainOpenMM   = 0.1 // millimetres in a quarter
	rainOpenProb = 40  // percent
	rainKeepMM   = 0.05
	rainKeepProb = 25
	rainLikely   = 70 // a wet row's chance at or over this says "likely"

	heatOpenC = 40.5 // feels-like °C: the LA-county heat-advisory line, ~105°F
	heatKeepC = 38.0 // ~100°F

	windOpenKmh = 56.0 // gusts: the NWS Wind Advisory line, 35 mph
	windKeepKmh = 45.0 // ~28 mph

	fireRHOpen   = 15.0 // Red Flag: humidity at or under this…
	fireRHKeep   = 20.0
	fireGustOpen = 56.0 // …while gusts reach the advisory line
	fireGustKeep = 45.0

	rainCap      = 4                // episodes opened per hazard per sliding 24h
	rainCooldown = 30 * time.Minute // a closed episode holds reopening this long
)

// hazardOrder is evaluation order: fire before wind, so the tick a red
// flag opens is the tick the plain wind push learns to stay quiet.
var hazardOrder = []string{"rain", "heat", "fire", "wind"}

// rainRow is one Open-Meteo quarter, stamped at its interval's end.
// Prob is -1 when the model gave none; the other fields are NaN then.
type rainRow struct {
	End  time.Time
	MM   float64
	Prob float64
	AppT float64 // feels-like, °C
	RH   float64 // relative humidity, percent
	Gust float64 // km/h
}

// rainPlace is the Weather list's first row: the app's own order — the
// fractional drag rank, falling back to created — over the undeleted items.
type rainPlace struct {
	ID   string
	Name string
	CC   string
	Lat  float64
	Lon  float64
}

// rainRec is one push that qualified — delivered, or held back by the cap.
type rainRec struct {
	T        time.Time `json:"t"`
	Place    string    `json:"place"`
	City     string    `json:"city"`
	Hazard   string    `json:"hazard"`         // "rain", "heat", "wind", "fire"
	Kind     string    `json:"kind"`           // "open" or "clear"
	Word     string    `json:"word,omitempty"` // rain: "possible" or "likely"
	MM       float64   `json:"mm,omitempty"`   // rain: the window's total
	Prob     int       `json:"prob,omitempty"` // rain: the wet rows' peak chance
	PeakT    float64   `json:"peak_t,omitempty"`  // heat: peak feels-like, °C
	PeakG    float64   `json:"peak_g,omitempty"`  // wind, fire: peak gust, km/h
	MinRH    float64   `json:"min_rh,omitempty"`  // fire: driest qualifying quarter
	HeldBack bool      `json:"held_back,omitempty"`
	Title    string    `json:"title"`
	Body     string    `json:"body"`
}

// rainEpisode is one hazard's open-or-quiet state and its day's budget.
type rainEpisode struct {
	Place    string      `json:"place,omitempty"`
	City     string      `json:"city,omitempty"`
	OpenedAt time.Time   `json:"opened_at,omitempty"`
	ClosedAt time.Time   `json:"closed_at,omitempty"`
	Opened   []time.Time `json:"opened,omitempty"` // openings, for the sliding-day cap
	Held     bool        `json:"held,omitempty"`   // a qualifying forecast waits on the cap
}

type rainState struct {
	Episodes map[string]*rainEpisode `json:"episodes"`
	LastOK   time.Time               `json:"last_ok,omitempty"`
	LastErr  string                  `json:"last_err,omitempty"`
}

func (s *Server) rainStatePath() string { return filepath.Join(s.StateDir, rainStateFile) }

// loadRainState also migrates the first build's file, whose rain episode
// lived at the top level before the hazards arrived.
func (s *Server) loadRainState() *rainState {
	st := &rainState{Episodes: map[string]*rainEpisode{}}
	b, err := os.ReadFile(s.rainStatePath())
	if err != nil {
		return st
	}
	var saved struct {
		rainState
		Place    string      `json:"place"`
		City     string      `json:"city"`
		OpenedAt time.Time   `json:"opened_at"`
		ClosedAt time.Time   `json:"closed_at"`
		Opened   []time.Time `json:"opened"`
		Held     bool        `json:"held"`
	}
	if json.Unmarshal(b, &saved) != nil {
		return st
	}
	out := saved.rainState
	if out.Episodes == nil {
		out.Episodes = map[string]*rainEpisode{}
	}
	if saved.Place != "" || len(saved.Opened) > 0 || !saved.ClosedAt.IsZero() {
		out.Episodes["rain"] = &rainEpisode{Place: saved.Place, City: saved.City,
			OpenedAt: saved.OpenedAt, ClosedAt: saved.ClosedAt, Opened: saved.Opened, Held: saved.Held}
	}
	return &out
}

func (s *Server) saveRainState(st *rainState) {
	b, err := json.Marshal(st)
	if err != nil {
		return
	}
	tmp := s.rainStatePath() + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, s.rainStatePath())
	}
}

// rainTopPlace reads Weather's places.json and returns the row the app
// lists first, or nil when the list is empty or unreadable.
func (s *Server) rainTopPlace() *rainPlace {
	b, err := os.ReadFile(filepath.Join(s.appDataDir("Weather"), "places.json"))
	if err != nil {
		return nil
	}
	var doc struct {
		Items []struct {
			ID      string   `json:"id"`
			Name    string   `json:"name"`
			CC      string   `json:"cc"`
			Lat     float64  `json:"lat"`
			Lon     float64  `json:"lon"`
			Order   *float64 `json:"order"`
			Created float64  `json:"created"`
			Deleted int64    `json:"deleted"`
		} `json:"items"`
	}
	if json.Unmarshal(b, &doc) != nil {
		return nil
	}
	type keyed struct {
		p   rainPlace
		key float64
		cr  float64
	}
	var best *keyed
	for _, it := range doc.Items {
		if it.Deleted != 0 || it.ID == "" {
			continue
		}
		k := keyed{p: rainPlace{ID: it.ID, Name: it.Name, CC: it.CC, Lat: it.Lat, Lon: it.Lon}, key: it.Created, cr: it.Created}
		if it.Order != nil {
			k.key = *it.Order
		}
		if best == nil || k.key < best.key ||
			(k.key == best.key && (k.cr < best.cr || (k.cr == best.cr && k.p.ID < best.p.ID))) {
			best = &k
		}
	}
	if best == nil {
		return nil
	}
	return &best.p
}

// rainWindowRows keeps the quarters whose interval (stamp−15m, stamp]
// overlaps the next 60 minutes — stamps after now and before now+75m,
// edge quarters taken whole.
func rainWindowRows(rows []rainRow, now time.Time) []rainRow {
	var win []rainRow
	for _, r := range rows {
		if r.End.After(now) && r.End.Before(now.Add(rainWindow+rainQuarter)) {
			win = append(win, r)
		}
	}
	return win
}

// rainWet is rain's joint floor, one row at a time; a missing probability
// (Prob < 0) leaves the millimetres to decide.
func rainWet(r rainRow, mm, prob float64) bool {
	return r.MM >= mm && (r.Prob < 0 || r.Prob >= prob)
}

// hazardHit says whether one quarter qualifies for a hazard — at the open
// floors, or at the lower keep floors that hold an episode open. A missing
// field never qualifies.
func hazardHit(h string, r rainRow, keep bool) bool {
	switch h {
	case "rain":
		if keep {
			return rainWet(r, rainKeepMM, rainKeepProb)
		}
		return rainWet(r, rainOpenMM, rainOpenProb)
	case "heat":
		t := heatOpenC
		if keep {
			t = heatKeepC
		}
		return !math.IsNaN(r.AppT) && r.AppT >= t
	case "wind":
		g := windOpenKmh
		if keep {
			g = windKeepKmh
		}
		return !math.IsNaN(r.Gust) && r.Gust >= g
	case "fire":
		rh, g := fireRHOpen, fireGustOpen
		if keep {
			rh, g = fireRHKeep, fireGustKeep
		}
		return !math.IsNaN(r.RH) && !math.IsNaN(r.Gust) && r.RH <= rh && r.Gust >= g
	}
	return false
}

// rainStep applies every hazard to one fresh, full window. Pure on the
// state, so the tests drive it with a clock. The caller has already made
// sure the window holds at least four quarters.
func rainStep(st *rainState, place *rainPlace, win []rainRow, now time.Time) []*rainRec {
	var recs []*rainRec
	for _, h := range hazardOrder {
		if h == "wind" {
			if f := st.Episodes["fire"]; f != nil && f.Place != "" {
				// a red-flag hour outranks the plain wind push: no new wind
				// episode opens under one (an already-open one may still close)
				if e := st.Episodes["wind"]; e == nil || e.Place == "" {
					continue
				}
			}
		}
		if rec := hazardStep(st, h, place, win, now); rec != nil {
			recs = append(recs, rec)
		}
	}
	return recs
}

// hazardStep is one hazard's episode at one poll.
func hazardStep(st *rainState, h string, place *rainPlace, win []rainRow, now time.Time) *rainRec {
	e := st.Episodes[h]
	if e == nil {
		e = &rainEpisode{}
		st.Episodes[h] = e
	}
	// a different first city drops the open episode silently: no clear push
	// for a place no longer home, and no cooldown carried over; the day's
	// budget stays, it belongs to the phone, not the city
	if e.Place != "" && e.Place != place.ID {
		e.Place, e.City, e.OpenedAt, e.ClosedAt, e.Held = "", "", time.Time{}, time.Time{}, false
	}

	if e.Place == "" {
		hit := false
		rec := rainRec{T: now.UTC(), Place: place.ID, City: place.Name, Hazard: h, Kind: "open",
			Prob: -1, PeakT: math.Inf(-1), PeakG: math.Inf(-1), MinRH: math.Inf(1)}
		for _, r := range win {
			if h == "rain" {
				rec.MM += r.MM
			}
			if !hazardHit(h, r, false) {
				continue
			}
			hit = true
			if h == "rain" && int(r.Prob) > rec.Prob {
				rec.Prob = int(r.Prob)
			}
			if !math.IsNaN(r.AppT) && r.AppT > rec.PeakT {
				rec.PeakT = r.AppT
			}
			if !math.IsNaN(r.Gust) && r.Gust > rec.PeakG {
				rec.PeakG = r.Gust
			}
			if !math.IsNaN(r.RH) && r.RH < rec.MinRH {
				rec.MinRH = r.RH
			}
		}
		if !hit {
			e.Held = false
			return nil
		}
		if h == "rain" {
			rec.Word = "possible"
			if rec.Prob >= rainLikely {
				rec.Word = "likely"
			}
		}
		// unused stat sentinels out before the record is marshalled
		if rec.Prob < 0 {
			rec.Prob = 0
		}
		if math.IsInf(rec.PeakT, -1) {
			rec.PeakT = 0
		}
		if math.IsInf(rec.PeakG, -1) {
			rec.PeakG = 0
		}
		if math.IsInf(rec.MinRH, 1) {
			rec.MinRH = 0
		}
		if now.Sub(e.ClosedAt) < rainCooldown {
			return nil
		}
		opened := e.Opened[:0]
		for _, t := range e.Opened {
			if now.Sub(t) < 24*time.Hour {
				opened = append(opened, t)
			}
		}
		e.Opened = opened
		if len(e.Opened) >= rainCap {
			if e.Held { // already noted once; wait quietly for the cap
				return nil
			}
			e.Held = true
			rec.HeldBack = true
			rec.Title, rec.Body = hazardText(&rec, place.CC == "US")
			return &rec
		}
		e.Held = false
		e.Place, e.City, e.OpenedAt = place.ID, place.Name, now.UTC()
		e.Opened = append(e.Opened, now.UTC())
		rec.Title, rec.Body = hazardText(&rec, place.CC == "US")
		return &rec
	}

	// open: only a fresh forecast with every quarter under the keep floors
	// closes the episode and earns the clear push
	for _, r := range win {
		if hazardHit(h, r, true) {
			return nil
		}
	}
	rec := rainRec{T: now.UTC(), Place: e.Place, City: e.City, Hazard: h, Kind: "clear"}
	e.Place, e.City, e.OpenedAt = "", "", time.Time{}
	e.ClosedAt = now.UTC()
	rec.Title, rec.Body = hazardText(&rec, place.CC == "US")
	return &rec
}

// fmtTemp and fmtGust speak the reader's units: the US in °F and mph, the
// rest of the world in °C and km/h. Rain stays in millimetres everywhere.
func fmtTemp(c float64, imperial bool) string {
	if imperial {
		return fmt.Sprintf("%.0f°F", c*9/5+32)
	}
	return fmt.Sprintf("%.0f°C", c)
}

func fmtGust(kmh float64, imperial bool) string {
	if imperial {
		return fmt.Sprintf("%.0f mph", kmh*0.621371)
	}
	return fmt.Sprintf("%.0f km/h", kmh)
}

// hazardText is the notification: the words the thread agreed for rain,
// their siblings for the SoCal three.
func hazardText(rec *rainRec, imperial bool) (string, string) {
	var title, body string
	switch {
	case rec.Kind == "clear":
		switch rec.Hazard {
		case "rain":
			return fmt.Sprintf("Next hour looks dry in %s", rec.City),
				"Rain has left the forecast's next hour."
		case "heat":
			return fmt.Sprintf("Heat easing in %s", rec.City),
				fmt.Sprintf("Feels-like is back under %s for the next hour.", fmtTemp(heatKeepC, imperial))
		case "wind":
			return fmt.Sprintf("Winds easing in %s", rec.City),
				fmt.Sprintf("Gusts are back under %s for the next hour.", fmtGust(windKeepKmh, imperial))
		case "fire":
			return fmt.Sprintf("Fire weather easing in %s", rec.City),
				"Wind and humidity are back off the red-flag line."
		}
	case rec.Hazard == "rain":
		title = fmt.Sprintf("Rain possible soon in %s", rec.City)
		if rec.Word == "likely" {
			title = fmt.Sprintf("Rain likely in %s in the next hour", rec.City)
		}
		body = fmt.Sprintf("Open-Meteo sees %.1f mm in the hour ahead", rec.MM)
		if rec.Prob > 0 {
			body += fmt.Sprintf(", chance up to %d%%", rec.Prob)
		}
	case rec.Hazard == "heat":
		title = fmt.Sprintf("Dangerous heat in %s", rec.City)
		body = fmt.Sprintf("Feels like %s in the hour ahead", fmtTemp(rec.PeakT, imperial))
	case rec.Hazard == "wind":
		title = fmt.Sprintf("High wind in %s", rec.City)
		body = fmt.Sprintf("Gusts to %s in the hour ahead", fmtGust(rec.PeakG, imperial))
	case rec.Hazard == "fire":
		title = fmt.Sprintf("Fire weather in %s", rec.City)
		body = fmt.Sprintf("Gusts to %s with humidity near %.0f%% — red-flag conditions in the hour ahead",
			fmtGust(rec.PeakG, imperial), rec.MinRH)
	}
	if rec.HeldBack {
		body += fmt.Sprintf(" · held back: the day's %d %s alerts are spent", rainCap, rec.Hazard)
	}
	return title, body
}

// rainForecast asks Open-Meteo for the quarter rows — rain and the SoCal
// hazards' fields in the one call, stamped in unix time so no timezone
// arithmetic is ours.
func rainForecast(ctx context.Context, lat, lon float64) ([]rainRow, error) {
	q := url.Values{
		"latitude":             {fmt.Sprintf("%.5f", lat)},
		"longitude":            {fmt.Sprintf("%.5f", lon)},
		"minutely_15":          {"precipitation,precipitation_probability,apparent_temperature,relative_humidity_2m,wind_gusts_10m"},
		"forecast_minutely_15": {"16"},
		"timeformat":           {"unixtime"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.open-meteo.com/v1/forecast?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("open-meteo: %s", resp.Status)
	}
	var out struct {
		Minutely struct {
			Time []int64    `json:"time"`
			MM   []*float64 `json:"precipitation"`
			Prob []*float64 `json:"precipitation_probability"`
			AppT []*float64 `json:"apparent_temperature"`
			RH   []*float64 `json:"relative_humidity_2m"`
			Gust []*float64 `json:"wind_gusts_10m"`
		} `json:"minutely_15"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("open-meteo: %v", err)
	}
	at := func(xs []*float64, i int) float64 {
		if i < len(xs) && xs[i] != nil {
			return *xs[i]
		}
		return math.NaN()
	}
	rows := make([]rainRow, 0, len(out.Minutely.Time))
	for i, t := range out.Minutely.Time {
		r := rainRow{End: time.Unix(t, 0), Prob: -1,
			AppT: at(out.Minutely.AppT, i), RH: at(out.Minutely.RH, i), Gust: at(out.Minutely.Gust, i)}
		if v := at(out.Minutely.MM, i); !math.IsNaN(v) {
			r.MM = v
		}
		if v := at(out.Minutely.Prob, i); !math.IsNaN(v) {
			r.Prob = v
		}
		rows = append(rows, r)
	}
	return rows, nil
}

// RunRain is the sampler: one Open-Meteo round every five minutes while a
// push subscription exists, then the rule. It runs for the daemon's life.
func (s *Server) RunRain(ctx context.Context) {
	st := s.loadRainState()
	s.rainMu.Lock()
	s.rain = st
	s.rainMu.Unlock()
	log.Printf("weather alerts: watching the Weather app's first city (rain, heat, wind, fire)")
	for ctx.Err() == nil {
		s.rainTick(ctx, time.Now())
		select {
		case <-ctx.Done():
			return
		case <-time.After(rainPoll):
		}
	}
}

func (s *Server) rainTick(ctx context.Context, now time.Time) {
	s.pushMu.Lock()
	subs := len(s.loadPushSubs())
	s.pushMu.Unlock()
	if subs == 0 {
		return // nobody to tap; the forecast can wait
	}
	place := s.rainTopPlace()
	s.rainMu.Lock()
	st := s.rain
	if st == nil {
		s.rainMu.Unlock()
		return
	}
	if place == nil {
		if len(st.Episodes) != 0 || st.LastErr != "no places in the Weather app" {
			st.Episodes = map[string]*rainEpisode{}
			st.LastErr = "no places in the Weather app"
			s.saveRainState(st)
		}
		s.rainMu.Unlock()
		return
	}
	s.rainMu.Unlock()

	rows, err := rainForecast(ctx, place.Lat, place.Lon)

	s.rainMu.Lock()
	defer s.rainMu.Unlock()
	if err != nil {
		st.LastErr = err.Error()
		s.saveRainState(st)
		return
	}
	win := rainWindowRows(rows, now)
	if len(win) < 4 { // the hour ahead needs five quarters; four keeps a ragged answer usable
		st.LastErr = "open-meteo: the forecast stops short of the next hour"
		s.saveRainState(st)
		return
	}
	st.LastOK, st.LastErr = now.UTC(), ""
	recs := rainStep(st, place, win, now)
	s.saveRainState(st)
	for _, rec := range recs {
		s.appendRainLog(rec)
		if rec.HeldBack {
			log.Printf("weather alerts: %s (held back, the day's %d are spent)", rec.Title, rainCap)
			continue
		}
		log.Printf("weather alerts: %s — %s", rec.Title, rec.Body)
		r := *rec
		go func() {
			n, errs := s.pushAll(context.Background(), pushMessage{Title: r.Title, Body: r.Body, Tag: r.Hazard + "-" + r.Place, URL: "/"})
			for _, e := range errs {
				log.Printf("weather alerts: push: %s", e)
			}
			if n > 0 {
				log.Printf("weather alerts: pushed to %d", n)
			}
		}()
	}
}

func (s *Server) appendRainLog(rec *rainRec) {
	f, err := os.OpenFile(filepath.Join(s.StateDir, rainLogFile), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	b, _ := json.Marshal(rec)
	f.Write(append(b, '\n'))
}
