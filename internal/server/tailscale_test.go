package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"exe/internal/config"
)

// fixtures in the shape of the real CLI answers on the dev host (1.102.3)
const tsFixStatus = `{
 "Version": "1.102.3-t9329c3677-ga522f65e9", "TUN": true, "BackendState": "Running", "AuthURL": "",
 "TailscaleIPs": ["100.116.32.57", "fd7a:115c:a1e0::7337:2039"],
 "Self": {"ID": "n8Gff", "HostName": "spark-7c78", "DNSName": "spark-7c78.tailnet-ac29.ts.net.", "OS": "linux",
   "TailscaleIPs": ["100.116.32.57", "fd7a:115c:a1e0::7337:2039"], "Online": true, "ExitNode": false, "ExitNodeOption": false,
   "KeyExpiry": "2026-11-07T18:57:38Z"},
 "Health": [], "MagicDNSSuffix": "tailnet-ac29.ts.net",
 "CurrentTailnet": {"Name": "v2ex.livid@me.com", "MagicDNSSuffix": "tailnet-ac29.ts.net", "MagicDNSEnabled": true},
 "ExitNodeStatus": null,
 "Peer": {
  "a": {"ID": "1", "HostName": "entropy", "DNSName": "entropy.tailnet-ac29.ts.net.", "OS": "linux", "TailscaleIPs": ["100.72.115.50", "fd7a::1"], "Online": true, "ExitNode": false, "ExitNodeOption": true, "LastSeen": "0001-01-01T00:00:00Z"},
  "b": {"ID": "2", "HostName": "localhost", "DNSName": "iphone181.tailnet-ac29.ts.net.", "OS": "iOS", "TailscaleIPs": ["100.107.26.68"], "Online": false, "ExitNode": false, "ExitNodeOption": false, "LastSeen": "2026-09-10T16:53:02.1Z"},
  "c": {"ID": "3", "HostName": "licht", "DNSName": "licht.tailnet-ac29.ts.net.", "OS": "linux", "TailscaleIPs": ["100.85.61.41"], "Online": true, "ExitNode": true, "ExitNodeOption": true, "LastSeen": "0001-01-01T00:00:00Z"},
  "d": {"ID": "4", "HostName": "mullvad", "DNSName": "us-lax-wg-001.mullvad.ts.net.", "OS": "linux", "TailscaleIPs": ["100.100.1.1"], "Online": true, "ExitNode": false, "ExitNodeOption": true, "Location": {"Country": "USA", "City": "Los Angeles"}}
 }}`

const tsFixPrefs = `{"ControlURL": "https://controlplane.tailscale.com", "RouteAll": false, "ExitNodeID": "3", "ExitNodeIP": "",
 "ExitNodeAllowLANAccess": true, "CorpDNS": true, "RunSSH": false, "WantRunning": true, "LoggedOut": false, "ShieldsUp": false,
 "AdvertiseRoutes": null, "OperatorUser": "livid"}`

const tsFixServe = `{"TCP": {"443": {"HTTPS": true}, "8788": {"HTTPS": true}, "10000": {"TCPForward": "127.0.0.1:22"}},
 "Web": {"spark-7c78.tailnet-ac29.ts.net:443": {"Handlers": {"/": {"Proxy": "http://127.0.0.1:7777"}}},
         "spark-7c78.tailnet-ac29.ts.net:8788": {"Handlers": {"/": {"Proxy": "http://100.116.32.57:7788"}}}},
 "AllowFunnel": {"spark-7c78.tailnet-ac29.ts.net:8788": true}}`

// fakeTailscale stands in for the CLI: canned answers, and a log of every
// command the daemon ran.
func fakeTailscale(t *testing.T, installed bool, status string) *[][]string {
	t.Helper()
	var ran [][]string
	oldBin, oldRun := tsBinary, tsRun
	tsBinary = func() string {
		if installed {
			return "/usr/bin/tailscale"
		}
		return ""
	}
	tsRun = func(ctx context.Context, args ...string) ([]byte, error) {
		ran = append(ran, args)
		switch strings.Join(args, " ") {
		case "status --json":
			if status == "" {
				return nil, errors.New("tailscale status: failed to connect to local tailscaled")
			}
			return []byte(status), nil
		case "debug prefs":
			return []byte(tsFixPrefs), nil
		case "serve status --json":
			return []byte(tsFixServe), nil
		}
		if args[0] == "set" || args[0] == "up" || args[0] == "down" {
			return nil, nil
		}
		return nil, errors.New("unexpected: " + strings.Join(args, " "))
	}
	t.Cleanup(func() { tsBinary, tsRun = oldBin, oldRun })
	return &ran
}

func tsGet(t *testing.T, s *Server, target, remote, host, xff string) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", target, nil)
	if remote != "" {
		req.RemoteAddr = remote
	}
	if host != "" {
		req.Host = host
	}
	if xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}
	s.handleTailscale(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: %d %s", target, rec.Code, rec.Body)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestTailscaleStatus(t *testing.T) {
	ran := fakeTailscale(t, true, tsFixStatus)
	s := New(&config.Config{Listen: "100.116.32.57:7777"}, nil, nil, "", t.TempDir())

	b := tsGet(t, s, "/v1/tailscale", "127.0.0.1:5000", "127.0.0.1:7777", "")
	if b["installed"] != true || b["state"] != "Running" || b["version"] != "1.102.3" {
		t.Fatalf("head: installed=%v state=%v version=%v", b["installed"], b["state"], b["version"])
	}
	self := b["self"].(map[string]any)
	if self["name"] != "spark-7c78" || self["dns_name"] != "spark-7c78.tailnet-ac29.ts.net" || self["ip"] != "100.116.32.57" || self["key_expiry"] == nil {
		t.Errorf("self: %v", self)
	}
	if b["peers_total"] != 4.0 || b["peers_online"] != 3.0 {
		t.Errorf("counts: %v of %v", b["peers_online"], b["peers_total"])
	}
	peers := b["peers"].([]any)
	names := []string{}
	for _, p := range peers {
		names = append(names, p.(map[string]any)["name"].(string))
	}
	if strings.Join(names, ",") != "entropy,iphone181,licht,us-lax-wg-001" { // sorted, DNS labels not hostnames
		t.Errorf("peer names: %v", names)
	}
	iphone := peers[1].(map[string]any)
	if iphone["online"] != false || iphone["last_seen"] == nil || iphone["os"] != "iOS" {
		t.Errorf("iphone: %v", iphone)
	}
	if peers[3].(map[string]any)["location"] != "Los Angeles, USA" {
		t.Errorf("mullvad location: %v", peers[3])
	}
	exit := b["exit_node"].(map[string]any)
	if exit["name"] != "licht" || exit["ip"] != "100.85.61.41" || exit["online"] != true {
		t.Errorf("exit node: %v", exit)
	}
	prefs := b["prefs"].(map[string]any)
	if prefs["accept_dns"] != true || prefs["accept_routes"] != false || prefs["exit_node_allow_lan_access"] != true || prefs["operator"] != "livid" {
		t.Errorf("prefs: %v", prefs)
	}
	serve := b["serve"].([]any)
	if len(serve) != 3 {
		t.Fatalf("serve rows: %v", serve)
	}
	r0, r1, r2 := serve[0].(map[string]any), serve[1].(map[string]any), serve[2].(map[string]any)
	if r0["url"] != "https://spark-7c78.tailnet-ac29.ts.net" || r0["target"] != "http://127.0.0.1:7777" || r0["public"] != false {
		t.Errorf("serve 0: %v", r0)
	}
	if r1["url"] != "https://spark-7c78.tailnet-ac29.ts.net:8788" || r1["public"] != true {
		t.Errorf("serve 1: %v", r1)
	}
	if r2["url"] != "tcp://spark-7c78.tailnet-ac29.ts.net:10000" || r2["target"] != "127.0.0.1:22" {
		t.Errorf("serve 2: %v", r2)
	}
	if b["via"] != "local" || b["listen_on_tailscale"] != true {
		t.Errorf("via=%v listen_on_tailscale=%v", b["via"], b["listen_on_tailscale"])
	}
	if b["detected"] == nil || b["checked_at"] == nil {
		t.Error("legacy detected key or checked_at missing")
	}

	// the cache answers the next one, and via is per request, not cached
	n := len(*ran)
	if b := tsGet(t, s, "/v1/tailscale", "192.168.1.20:5000", "192.168.1.115:7777", ""); b["via"] != "lan" || len(*ran) != n {
		t.Errorf("cached lan request: via=%v ran=%d", b["via"], len(*ran)-n)
	}
	if b := tsGet(t, s, "/v1/tailscale", "100.76.117.118:5000", "100.116.32.57:7777", ""); b["via"] != "tailscale" {
		t.Errorf("tailnet address: via=%v", b["via"])
	}
	if b := tsGet(t, s, "/v1/tailscale", "127.0.0.1:5000", "spark-7c78.tailnet-ac29.ts.net", ""); b["via"] != "tailscale" {
		t.Errorf("serve host: via=%v", b["via"])
	}
	if b := tsGet(t, s, "/v1/tailscale", "127.0.0.1:5000", "127.0.0.1:7777", "100.76.117.118"); b["via"] != "tailscale" {
		t.Errorf("forwarded tailnet address: via=%v", b["via"])
	}
	if tsGet(t, s, "/v1/tailscale?force=1", "", "", ""); len(*ran) != n+3 {
		t.Errorf("force: ran %d more commands, want 3", len(*ran)-n)
	}
}

func TestTailscaleAbsent(t *testing.T) {
	fakeTailscale(t, false, "")
	s := New(&config.Config{Listen: "127.0.0.1:7777"}, nil, nil, "", t.TempDir())
	b := tsGet(t, s, "/v1/tailscale", "", "", "")
	if b["installed"] != false || b["state"] != nil || b["listen_on_tailscale"] != false {
		t.Errorf("absent: %v", b)
	}
	rec := httptest.NewRecorder()
	s.handleTailscaleSet(rec, httptest.NewRequest("POST", "/v1/tailscale/set", strings.NewReader(`{"action":"down"}`)))
	if rec.Code != http.StatusNotFound {
		t.Errorf("set without a CLI: %d", rec.Code)
	}
}

func TestTailscaleNoDaemon(t *testing.T) {
	fakeTailscale(t, true, "")
	s := New(&config.Config{}, nil, nil, "", t.TempDir())
	b := tsGet(t, s, "/v1/tailscale", "", "", "")
	if b["installed"] != true || b["state"] != "NoDaemon" || !strings.Contains(b["error"].(string), "tailscaled") {
		t.Errorf("no daemon: %v", b)
	}
}

func TestTailscaleSet(t *testing.T) {
	ran := fakeTailscale(t, true, tsFixStatus)
	s := New(&config.Config{}, nil, nil, "", t.TempDir())
	post := func(body string) (int, map[string]any, []string) {
		*ran = nil
		rec := httptest.NewRecorder()
		s.handleTailscaleSet(rec, httptest.NewRequest("POST", "/v1/tailscale/set", strings.NewReader(body)))
		var b map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &b)
		var cmd []string
		for _, r := range *ran {
			if r[0] != "status" && r[0] != "debug" && r[0] != "serve" {
				cmd = r
			}
		}
		return rec.Code, b, cmd
	}
	cases := []struct{ body, want string }{
		{`{"action":"up"}`, "up --timeout=15s"},
		{`{"action":"down"}`, "down --accept-risk=all"},
		{`{"action":"login"}`, "up --timeout=3s"},
		{`{"action":"set","key":"accept_routes","value":true}`, "set --accept-routes=true --accept-risk=all"},
		{`{"action":"set","key":"accept_dns","value":false}`, "set --accept-dns=false --accept-risk=all"},
		{`{"action":"set","key":"shields_up","value":true}`, "set --shields-up=true --accept-risk=all"},
		{`{"action":"set","key":"ssh","value":true}`, "set --ssh=true --accept-risk=all"},
		{`{"action":"set","key":"exit_node_allow_lan_access","value":false}`, "set --exit-node-allow-lan-access=false --accept-risk=all"},
		{`{"action":"set","key":"exit_node","value":""}`, "set --exit-node= --accept-risk=all"},
		{`{"action":"set","key":"exit_node","value":"100.72.115.50"}`, "set --exit-node=100.72.115.50 --accept-risk=all"},
		{`{"action":"set","key":"exit_node","value":"Entropy"}`, "set --exit-node=Entropy --accept-risk=all"},
	}
	for _, c := range cases {
		code, b, cmd := post(c.body)
		if code != http.StatusOK {
			t.Errorf("%s: %d %v", c.body, code, b)
			continue
		}
		if got := strings.Join(cmd, " "); got != c.want {
			t.Errorf("%s: ran %q, want %q", c.body, got, c.want)
		}
		if b["state"] != "Running" || b["via"] == nil {
			t.Errorf("%s: answer is not the fresh status: %v", c.body, b)
		}
	}
	// refused: unknown actions and keys, wrong value types, a device that is
	// not an exit node (licht's iPhone) or is not on the tailnet at all
	for _, body := range []string{
		`{"action":"funnel"}`, `{"action":"set","key":"hostname","value":"x"}`, `{"action":"set","key":"advertise_exit_node","value":true}`,
		`{"action":"set","key":"accept_routes","value":"yes"}`, `{"action":"set","key":"exit_node","value":true}`,
		`{"action":"set","key":"exit_node","value":"iphone181"}`, `{"action":"set","key":"exit_node","value":"8.8.8.8"}`,
		`{"action":"set","key":"exit_node","value":"--reset"}`, `not json`,
	} {
		code, _, cmd := post(body)
		if code != http.StatusBadRequest || cmd != nil {
			t.Errorf("%s: want 400 and no command, got %d %v", body, code, cmd)
		}
	}
	// the CLI's own words come back when it refuses
	old := tsRun
	tsRun = func(ctx context.Context, args ...string) ([]byte, error) {
		if args[0] == "set" {
			return nil, errors.New(`tailscale set: exit node "entropy" is offline`)
		}
		return old(ctx, args...)
	}
	code, b, _ := post(`{"action":"set","key":"exit_node","value":"entropy"}`)
	tsRun = old
	if code != http.StatusBadGateway || !strings.Contains(b["error"].(string), "offline") {
		t.Errorf("CLI refusal: %d %v", code, b)
	}
}

func TestTailscaleParseServeEmpty(t *testing.T) {
	if rows := tsParseServe([]byte("No serve config\n"), "x.ts.net"); len(rows) != 0 {
		t.Errorf("want no rows, got %v", rows)
	}
	rows := tsParseServe([]byte(`{"TCP":{"80":{"HTTP":true}},"Web":{"x.ts.net:80":{"Handlers":{"/docs":{"Path":"/srv/docs"},"/":{"Text":"hello"}}}}}`), "x.ts.net")
	if len(rows) != 2 || rows[0]["url"] != "http://x.ts.net" || rows[0]["target"] != "text: hello" || rows[1]["url"] != "http://x.ts.net/docs" || rows[1]["target"] != "/srv/docs" {
		t.Errorf("http rows: %v", rows)
	}
}
