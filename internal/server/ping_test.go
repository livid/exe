package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"exe/internal/config"
)

func TestPingEndpoints(t *testing.T) {
	s := New(config.Default(), nil, nil, "", t.TempDir())
	h := s.Handler()
	do := func(method, path, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
		return w
	}
	if w := do("POST", "/v1/ping/targets", `{"host":"-f /etc/passwd"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("a flag for a host: %d %s", w.Code, w.Body)
	}
	w := do("POST", "/v1/ping/targets", `{"host":"1.1.1.1","title":"Cloudflare"}`)
	var tg struct{ ID, Host, Title string }
	if w.Code != http.StatusCreated || json.Unmarshal(w.Body.Bytes(), &tg) != nil || tg.Host != "1.1.1.1" || tg.Title != "Cloudflare" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w := do("POST", "/v1/ping/targets", `{"host":"1.1.1.1"}`); w.Code != http.StatusConflict {
		t.Fatalf("twice: %d", w.Code)
	}
	if w := do("PUT", "/v1/ping/targets/"+tg.ID, `{"title":"One"}`); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"title":"One"`) {
		t.Fatalf("rename: %d %s", w.Code, w.Body)
	}

	w = do("GET", "/v1/ping", "")
	var st struct {
		Step, Pings int
		Targets     []struct{ ID, Title string }
		Ranges      []struct{ Title string }
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &st) != nil || st.Step != 300 || st.Pings != 20 ||
		len(st.Targets) != 1 || st.Targets[0].Title != "One" || len(st.Ranges) != 4 || st.Ranges[3].Title != "Last 360 Days" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("the status is cached")
	}

	for _, q := range []string{"range=60", "range=36000", "overview=1&range=10800", "start=200&end=100", "overview=1&start=1&end=2"} {
		if w := do("GET", "/v1/ping/targets/"+tg.ID+"/graph?"+q, ""); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", q, w.Code)
		}
	}
	// measured not yet: no history to draw (or no rrdtool to draw it)
	if w := do("GET", "/v1/ping/targets/"+tg.ID+"/graph?range=10800", ""); w.Code != http.StatusNotFound && w.Code != http.StatusServiceUnavailable {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w := do("DELETE", "/v1/ping/targets/"+tg.ID, ""); w.Code != http.StatusNoContent {
		t.Fatalf("%d", w.Code)
	}
	if w := do("DELETE", "/v1/ping/targets/"+tg.ID, ""); w.Code != http.StatusNotFound {
		t.Fatalf("%d", w.Code)
	}
}
