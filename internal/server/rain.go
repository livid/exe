package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

// Rain alerts for the Weather app's first city. A sampler polls Open-Meteo's
// quarter-hour rows for the top place in Weather's list — the same row the
// app draws first, the drag rank deciding — and sends a Web Push when rain
// reaches the next 60 minutes, and one more when a fresh forecast clears it.
// The rule, shaped in the hub thread that asked for it: the quarter rows,
// not the hourly ones (a precipitation stamp is its interval's end, so the
// first hour row is the hour already over); the window test is overlap,
// edge quarters taken whole; a quarter is wet only when millimetres and
// probability clear their floors together, checked row by row; probability
// picks the word, possible under the line, likely over it. A failed poll or
// a forecast that comes back short writes nothing: silence never counts as
// dry, and only a fresh all-dry forecast closes an open episode.

const (
	rainPoll      = 5 * time.Minute
	rainWindow    = time.Hour
	rainQuarter   = 15 * time.Minute
	rainStateFile = "rain-state.json"
	rainLogFile   = "rain.jsonl"

	// A quarter is wet at the open floors; an open episode stays open down
	// to the keep floors, so a shower flickering around the open line does
	// not tap twice. The probability rows are hourly-grade however fine the
	// quarters look (drawn as a line between the hours), so the floors are
	// set for that, and a row without a probability passes on millimetres
	// alone.
	rainOpenMM   = 0.1 // millimetres in a quarter
	rainOpenProb = 40  // percent
	rainKeepMM   = 0.05
	rainKeepProb = 25
	rainLikely   = 70 // a wet row's chance at or over this says "likely"

	rainCap      = 4                // episodes opened per sliding 24h
	rainCooldown = 30 * time.Minute // a closed episode holds reopening this long
)

// rainRow is one Open-Meteo quarter, stamped at its interval's end.
// Prob is -1 when the model gave none.
type rainRow struct {
	End  time.Time
	MM   float64
	Prob float64
}

// rainPlace is the Weather list's first row: the app's own order — the
// fractional drag rank, falling back to created — over the undeleted items.
type rainPlace struct {
	ID   string
	Name string
	Lat  float64
	Lon  float64
}

// rainRec is one push that qualified — delivered, or held back by the cap.
type rainRec struct {
	T        time.Time `json:"t"`
	Place    string    `json:"place"`
	City     string    `json:"city"`
	Kind     string    `json:"kind"`           // "rain" or "dry"
	Word     string    `json:"word,omitempty"` // "possible" or "likely"
	MM       float64   `json:"mm,omitempty"`   // the window's total
	Prob     int       `json:"prob,omitempty"` // the wet rows' peak chance
	HeldBack bool      `json:"held_back,omitempty"`
	Title    string    `json:"title"`
	Body     string    `json:"body"`
}

type rainState struct {
	Place    string      `json:"place,omitempty"` // open episode's place id ("" = none)
	City     string      `json:"city,omitempty"`
	OpenedAt time.Time   `json:"opened_at,omitempty"`
	ClosedAt time.Time   `json:"closed_at,omitempty"`
	Opened   []time.Time `json:"opened,omitempty"` // openings, for the sliding-day cap
	Held     bool        `json:"held,omitempty"`   // a wet forecast waits on the cap
	LastOK   time.Time   `json:"last_ok,omitempty"`
	LastErr  string      `json:"last_err,omitempty"`
}

func (s *Server) rainStatePath() string { return filepath.Join(s.StateDir, rainStateFile) }

func (s *Server) loadRainState() *rainState {
	st := &rainState{}
	if b, err := os.ReadFile(s.rainStatePath()); err == nil {
		var saved rainState
		if json.Unmarshal(b, &saved) == nil {
			return &saved
		}
	}
	return st
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
		k := keyed{p: rainPlace{ID: it.ID, Name: it.Name, Lat: it.Lat, Lon: it.Lon}, key: it.Created, cr: it.Created}
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

// rainWet is the joint floor, one row at a time; a missing probability
// (Prob < 0) leaves the millimetres to decide.
func rainWet(r rainRow, mm, prob float64) bool {
	return r.MM >= mm && (r.Prob < 0 || r.Prob >= prob)
}

// rainStep applies the rule to one fresh, full window. Pure on the state,
// so the tests drive it with a clock. The caller has already made sure the
// window holds at least four quarters.
func rainStep(st *rainState, place *rainPlace, win []rainRow, now time.Time) *rainRec {
	// a different first city drops the open episode silently: no dry push
	// for a place no longer home, and no cooldown carried over
	if st.Place != "" && st.Place != place.ID {
		st.Place, st.City, st.OpenedAt, st.ClosedAt, st.Held = "", "", time.Time{}, time.Time{}, false
	}

	if st.Place == "" {
		wet, peak, total := false, -1.0, 0.0
		for _, r := range win {
			total += r.MM
			if rainWet(r, rainOpenMM, rainOpenProb) {
				wet = true
				if r.Prob > peak {
					peak = r.Prob
				}
			}
		}
		if !wet {
			st.Held = false
			return nil
		}
		if now.Sub(st.ClosedAt) < rainCooldown {
			return nil
		}
		opened := st.Opened[:0]
		for _, t := range st.Opened {
			if now.Sub(t) < 24*time.Hour {
				opened = append(opened, t)
			}
		}
		st.Opened = opened
		rec := rainRec{T: now.UTC(), Place: place.ID, City: place.Name, Kind: "rain", MM: total}
		if peak >= 0 {
			rec.Prob = int(peak)
		}
		rec.Word = "possible"
		if peak >= rainLikely {
			rec.Word = "likely"
		}
		if len(st.Opened) >= rainCap {
			if st.Held { // already noted once; wait quietly for the cap
				return nil
			}
			st.Held = true
			rec.HeldBack = true
			rec.Title, rec.Body = rainText(&rec)
			return &rec
		}
		st.Held = false
		st.Place, st.City, st.OpenedAt = place.ID, place.Name, now.UTC()
		st.Opened = append(st.Opened, now.UTC())
		rec.Title, rec.Body = rainText(&rec)
		return &rec
	}

	// open: only a fresh forecast with every quarter dry at the keep floors
	// closes the episode and earns the dry push
	for _, r := range win {
		if rainWet(r, rainKeepMM, rainKeepProb) {
			return nil
		}
	}
	rec := rainRec{T: now.UTC(), Place: st.Place, City: st.City, Kind: "dry"}
	st.Place, st.City, st.OpenedAt = "", "", time.Time{}
	st.ClosedAt = now.UTC()
	rec.Title, rec.Body = rainText(&rec)
	return &rec
}

// rainText is the notification: the words agreed in the thread, the chance
// naming the word and the body carrying the window's numbers.
func rainText(rec *rainRec) (string, string) {
	if rec.Kind == "dry" {
		return fmt.Sprintf("Next hour looks dry in %s", rec.City),
			"Rain has left the forecast's next hour."
	}
	title := fmt.Sprintf("Rain possible soon in %s", rec.City)
	if rec.Word == "likely" {
		title = fmt.Sprintf("Rain likely in %s in the next hour", rec.City)
	}
	body := fmt.Sprintf("Open-Meteo sees %.1f mm in the hour ahead", rec.MM)
	if rec.Prob > 0 {
		body += fmt.Sprintf(", chance up to %d%%", rec.Prob)
	}
	if rec.HeldBack {
		body += " · held back: the day's 4 rain alerts are spent"
	}
	return title, body
}

// rainForecast asks Open-Meteo for the quarter rows: precipitation and its
// probability, stamped in unix time so no timezone arithmetic is ours.
func rainForecast(ctx context.Context, lat, lon float64) ([]rainRow, error) {
	q := url.Values{
		"latitude":             {fmt.Sprintf("%.5f", lat)},
		"longitude":            {fmt.Sprintf("%.5f", lon)},
		"minutely_15":          {"precipitation,precipitation_probability"},
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
		} `json:"minutely_15"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("open-meteo: %v", err)
	}
	rows := make([]rainRow, 0, len(out.Minutely.Time))
	for i, t := range out.Minutely.Time {
		r := rainRow{End: time.Unix(t, 0), Prob: -1}
		if i < len(out.Minutely.MM) && out.Minutely.MM[i] != nil {
			r.MM = *out.Minutely.MM[i]
		}
		if i < len(out.Minutely.Prob) && out.Minutely.Prob[i] != nil {
			r.Prob = *out.Minutely.Prob[i]
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
	log.Printf("rain alerts: watching the Weather app's first city")
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
		if st.Place != "" || st.LastErr != "no places in the Weather app" {
			st.Place, st.City, st.OpenedAt = "", "", time.Time{}
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
	rec := rainStep(st, place, win, now)
	s.saveRainState(st)
	if rec == nil {
		return
	}
	s.appendRainLog(rec)
	if rec.HeldBack {
		log.Printf("rain alerts: %s (held back, the day's %d are spent)", rec.Title, rainCap)
		return
	}
	log.Printf("rain alerts: %s — %s", rec.Title, rec.Body)
	r := *rec
	go func() {
		n, errs := s.pushAll(context.Background(), pushMessage{Title: r.Title, Body: r.Body, Tag: "rain-" + r.Place, URL: "/"})
		for _, e := range errs {
			log.Printf("rain alerts: push: %s", e)
		}
		if n > 0 {
			log.Printf("rain alerts: pushed to %d", n)
		}
	}()
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
