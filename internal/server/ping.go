package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"exe/internal/ping"
)

// The Ping app's daemon side: SmokePing's measurements, made here
// (internal/ping). The list of destinations and their round-robin
// databases are in <state>/ping.
//
//	GET    /v1/ping                         how it stands, each destination's last round
//	POST   /v1/ping/targets                 {"host": "…", "title": "…"}
//	PUT    /v1/ping/targets/{id}            {"host": "…"} or {"title": "…"}
//	DELETE /v1/ping/targets/{id}            and its history
//	GET    /v1/ping/targets/{id}/graph      a PNG drawn by rrdtool graph
//
// A graph is ?range=<seconds> back from now, one of the detail graphs
// (10800, 108000, 864000, 31104000) or the overview's 36000 with
// &overview=1, or &start=&end= (unix seconds) for a span of its own;
// &width= and &height= are the picture's CSS pixels and &zoom= the device
// pixel ratio. X-Ping-Plot says where its plotting area is, in CSS pixels,
// and the times at its edges: "left top width height start end".

func (s *Server) pingMonitor() *ping.Monitor {
	s.pingOnce.Do(func() { s.ping = ping.New(filepath.Join(s.StateDir, "ping")) })
	return s.ping
}

// RunPing measures the destinations until ctx ends.
func (s *Server) RunPing(ctx context.Context) { s.pingMonitor().Run(ctx) }

func (s *Server) handlePingStatus(w http.ResponseWriter, r *http.Request) {
	st := s.pingMonitor().Status()
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"rrdtool": st.RRDtool, "probe": st.Probe, "step": st.Step, "pings": st.Pings, "from": st.From,
		"round": st.Round, "next": st.Next, "busy": st.Busy, "targets": st.Targets,
		"ranges": ping.Ranges, "overview": ping.OverviewRange,
		"min_width": ping.MinWidth, "wide_width": ping.WideWidth,
	})
}

func pingErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ping.ErrBadHost):
		writeErr(w, http.StatusBadRequest, err)
	case errors.Is(err, ping.ErrExists):
		writeErr(w, http.StatusConflict, err)
	case errors.Is(err, ping.ErrNotFound):
		writeErr(w, http.StatusNotFound, err)
	case errors.Is(err, ping.ErrNoRRD):
		writeErr(w, http.StatusServiceUnavailable, err)
	default:
		writeErr(w, http.StatusInternalServerError, err)
	}
}

func (s *Server) handlePingAdd(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Host  string `json:"host"`
		Title string `json:"title"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	t, err := s.pingMonitor().Add(req.Host, req.Title)
	if err != nil {
		pingErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

func (s *Server) handlePingEdit(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Host  *string `json:"host"`
		Title *string `json:"title"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	t, err := s.pingMonitor().Edit(r.PathValue("id"), req.Host, req.Title)
	if err != nil {
		pingErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handlePingDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.pingMonitor().Delete(r.PathValue("id")); err != nil {
		pingErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handlePingGraph(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	num := func(k string) int64 { n, _ := strconv.ParseInt(q.Get(k), 10, 64); return n }
	o := ping.GraphOpts{
		Overview: q.Get("overview") == "1",
		Range:    num("range"),
		Start:    num("start"),
		End:      num("end"),
		Width:    int(num("width")),
		Height:   int(num("height")),
	}
	o.Zoom, _ = strconv.ParseFloat(q.Get("zoom"), 64)
	if o.Start > 0 || o.End > 0 {
		if o.Overview || o.Start <= 0 || o.End <= o.Start {
			writeErr(w, http.StatusBadRequest, errors.New("a span needs a start before its end"))
			return
		}
	} else if !pingRange(o) {
		writeErr(w, http.StatusBadRequest, errors.New("no such range"))
		return
	}
	png, plot, err := s.pingMonitor().Graph(r.Context(), r.PathValue("id"), o)
	if err != nil {
		pingErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Ping-Plot", strings.Join([]string{
		strconv.Itoa(plot.Left), strconv.Itoa(plot.Top), strconv.Itoa(plot.Width), strconv.Itoa(plot.Height),
		strconv.FormatInt(plot.Start, 10), strconv.FormatInt(plot.End, 10),
	}, " "))
	w.Write(png)
}

func pingRange(o ping.GraphOpts) bool {
	if o.Overview {
		return o.Range == ping.OverviewRange
	}
	for _, r := range ping.Ranges {
		if r.Seconds == o.Range {
			return true
		}
	}
	return false
}
