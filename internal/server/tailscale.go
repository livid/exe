package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The Control Strip's Tailscale module. The daemon asks the tailscale CLI on
// this machine — its status, its preferences and the Serve table — and
// answers every open desktop from one short cache; the module's menu
// changes settings through the same CLI (the daemon's user is tailscaled's
// operator, so no sudo). Only the settings listed in tsBoolFlags and the
// exit node can be changed, and an exit node must be a device that offers
// one: the daemon never becomes a general shell for the CLI. Turning the
// tailnet off is the module's business too, but the desktop itself may
// hang off the tailnet (the daemon listens on the Tailscale IP, or the
// browser came in through Tailscale Serve), so every answer says how the
// request arrived and the desktop asks before pulling that plug.

const tsTTL = 10 * time.Second

// tsBinary finds the CLI; empty when Tailscale is not installed here.
var tsBinary = func() string {
	if p, err := exec.LookPath("tailscale"); err == nil {
		return p
	}
	for _, p := range []string{"/usr/bin/tailscale", "/usr/local/bin/tailscale", "/opt/homebrew/bin/tailscale",
		"/Applications/Tailscale.app/Contents/MacOS/Tailscale", `C:\Program Files\Tailscale\tailscale.exe`} {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

// tsRun runs one tailscale command. A failure carries the CLI's own first
// line, which is what the desktop shows.
var tsRun = func(ctx context.Context, args ...string) ([]byte, error) {
	bin := tsBinary()
	if bin == "" {
		return nil, errors.New("tailscale is not installed")
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = strings.TrimSpace(out.String())
		}
		if msg == "" {
			msg = err.Error()
		}
		return out.Bytes(), fmt.Errorf("tailscale %s: %s", args[0], firstLine(msg, 160))
	}
	return out.Bytes(), nil
}

// the parts of `tailscale status --json` (ipnstate.Status) the module reads
type tsStatus struct {
	Version        string
	BackendState   string
	AuthURL        string
	TailscaleIPs   []string
	Self           *tsPeer
	Health         []string
	MagicDNSSuffix string
	CurrentTailnet *struct {
		Name           string
		MagicDNSSuffix string
	}
	ExitNodeStatus *struct {
		ID           string
		Online       bool
		TailscaleIPs []string
	}
	Peer map[string]*tsPeer
}

type tsPeer struct {
	ID             string
	HostName       string
	DNSName        string
	OS             string
	TailscaleIPs   []string
	Online         bool
	ExitNode       bool
	ExitNodeOption bool
	LastSeen       time.Time
	KeyExpiry      *time.Time
	Location       *struct {
		Country string
		City    string
	}
}

// the parts of `tailscale debug prefs` (ipn.Prefs) the module reads
type tsPrefs struct {
	RouteAll               bool
	ExitNodeID             string
	ExitNodeIP             string
	ExitNodeAllowLANAccess bool
	CorpDNS                bool
	RunSSH                 bool
	WantRunning            bool
	LoggedOut              bool
	ShieldsUp              bool
	AdvertiseRoutes        []string
	OperatorUser           string
}

// `tailscale serve status --json` (ipn.ServeConfig)
type tsServeConfig struct {
	TCP map[string]struct {
		HTTPS      bool
		HTTP       bool
		TCPForward string
	}
	Web map[string]struct {
		Handlers map[string]struct {
			Proxy string
			Path  string
			Text  string
		}
	}
	AllowFunnel map[string]bool
}

// the settings the menu toggles, and their CLI flags
var tsBoolFlags = map[string]string{
	"accept_routes":              "--accept-routes",
	"accept_dns":                 "--accept-dns",
	"shields_up":                 "--shields-up",
	"ssh":                        "--ssh",
	"exit_node_allow_lan_access": "--exit-node-allow-lan-access",
}

// tsName is a device's tailnet name: the first label of its MagicDNS name
// (an iPhone's HostName is "localhost"), else the hostname.
func tsName(p *tsPeer) string {
	if p == nil {
		return ""
	}
	if i := strings.IndexByte(p.DNSName, '.'); i > 0 {
		return p.DNSName[:i]
	}
	if p.DNSName != "" {
		return strings.TrimSuffix(p.DNSName, ".")
	}
	return p.HostName
}

func tsIPv4(ips []string) string {
	for _, ip := range ips {
		if p := net.ParseIP(ip); p != nil && p.To4() != nil {
			return ip
		}
	}
	if len(ips) > 0 {
		return ips[0]
	}
	return ""
}

func tsPeerJSON(p *tsPeer) map[string]any {
	m := map[string]any{
		"name": tsName(p), "dns_name": strings.TrimSuffix(p.DNSName, "."), "ip": tsIPv4(p.TailscaleIPs),
		"os": p.OS, "online": p.Online, "exit_node": p.ExitNode, "exit_node_option": p.ExitNodeOption,
	}
	if !p.Online && !p.LastSeen.IsZero() && p.LastSeen.Year() > 1 {
		m["last_seen"] = p.LastSeen.UTC()
	}
	if p.Location != nil && (p.Location.City != "" || p.Location.Country != "") {
		loc := p.Location.City
		if p.Location.Country != "" {
			if loc != "" {
				loc += ", "
			}
			loc += p.Location.Country
		}
		m["location"] = loc
	}
	return m
}

// tsParseServe flattens the Serve table into rows the menu can list: one
// per handler path, plus raw TCP forwards; public marks a Funnel port.
func tsParseServe(raw []byte, dnsName string) []map[string]any {
	rows := []map[string]any{}
	var sc tsServeConfig
	if json.Unmarshal(raw, &sc) != nil {
		return rows
	}
	hosts := make([]string, 0, len(sc.Web))
	for h := range sc.Web {
		hosts = append(hosts, h)
	}
	sort.Strings(hosts)
	for _, hp := range hosts {
		host, port, err := net.SplitHostPort(hp)
		if err != nil {
			host, port = hp, "443"
		}
		scheme := "https://"
		if t, ok := sc.TCP[port]; ok && t.HTTP && !t.HTTPS {
			scheme = "http://"
		}
		base := scheme + host
		if !(scheme == "https://" && port == "443") && !(scheme == "http://" && port == "80") {
			base += ":" + port
		}
		paths := make([]string, 0, len(sc.Web[hp].Handlers))
		for p := range sc.Web[hp].Handlers {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		for _, p := range paths {
			h := sc.Web[hp].Handlers[p]
			target := h.Proxy
			if target == "" && h.Path != "" {
				target = h.Path
			}
			if target == "" && h.Text != "" {
				target = "text: " + firstLine(h.Text, 40)
			}
			url := base
			if p != "/" {
				url += p
			}
			rows = append(rows, map[string]any{"url": url, "target": target, "public": sc.AllowFunnel[hp]})
		}
	}
	ports := make([]string, 0, len(sc.TCP))
	for p := range sc.TCP {
		ports = append(ports, p)
	}
	sort.Strings(ports)
	for _, port := range ports {
		t := sc.TCP[port]
		if t.TCPForward == "" {
			continue
		}
		hp := dnsName + ":" + port
		rows = append(rows, map[string]any{"url": "tcp://" + hp, "target": t.TCPForward, "public": sc.AllowFunnel[hp]})
	}
	return rows
}

// tsCollect asks the CLI and shapes the module's answer. installed:false
// when there is no CLI; state "NoDaemon" when it cannot reach tailscaled.
func tsCollect(ctx context.Context) map[string]any {
	ip := TailscaleIP()
	res := map[string]any{"detected": ip != "", "ip": ip, "installed": false, "checked_at": time.Now().UTC()}
	if tsBinary() == "" {
		return res
	}
	res["installed"] = true
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	out, err := tsRun(ctx, "status", "--json")
	var st tsStatus
	if len(bytes.TrimSpace(out)) == 0 || json.Unmarshal(out, &st) != nil {
		res["state"] = "NoDaemon"
		if err != nil {
			res["error"] = err.Error()
		} else {
			res["error"] = "tailscale status: unreadable answer"
		}
		return res
	}
	res["state"] = st.BackendState
	res["version"] = strings.SplitN(st.Version, "-", 2)[0]
	if st.AuthURL != "" {
		res["auth_url"] = st.AuthURL
	}
	if st.Health == nil {
		st.Health = []string{}
	}
	res["health"] = st.Health
	suffix := st.MagicDNSSuffix
	tailnet := map[string]any{"magic_dns_suffix": suffix}
	if st.CurrentTailnet != nil {
		tailnet["name"] = st.CurrentTailnet.Name
		if st.CurrentTailnet.MagicDNSSuffix != "" {
			tailnet["magic_dns_suffix"] = st.CurrentTailnet.MagicDNSSuffix
		}
	}
	res["tailnet"] = tailnet
	selfDNS := ""
	if st.Self != nil {
		self := tsPeerJSON(st.Self)
		self["ips"] = st.Self.TailscaleIPs
		if st.Self.KeyExpiry != nil {
			self["key_expiry"] = st.Self.KeyExpiry.UTC()
		}
		delete(self, "exit_node")
		res["self"] = self
		selfDNS = strings.TrimSuffix(st.Self.DNSName, ".")
	}

	peers := make([]map[string]any, 0, len(st.Peer))
	online := 0
	var exitNode map[string]any
	for _, p := range st.Peer {
		if p == nil {
			continue
		}
		pj := tsPeerJSON(p)
		peers = append(peers, pj)
		if p.Online {
			online++
		}
		if p.ExitNode {
			exitNode = map[string]any{"name": pj["name"], "ip": pj["ip"], "online": p.Online}
		}
	}
	sort.Slice(peers, func(i, j int) bool {
		return strings.ToLower(peers[i]["name"].(string)) < strings.ToLower(peers[j]["name"].(string))
	})
	res["peers"], res["peers_online"], res["peers_total"] = peers, online, len(peers)
	if exitNode == nil && st.ExitNodeStatus != nil {
		exitNode = map[string]any{"name": "", "ip": tsIPv4(st.ExitNodeStatus.TailscaleIPs), "online": st.ExitNodeStatus.Online}
	}
	res["exit_node"] = exitNode // null when the machine's traffic leaves on its own

	if out, err := tsRun(ctx, "debug", "prefs"); err == nil {
		var p tsPrefs
		if json.Unmarshal(out, &p) == nil {
			advExit := false
			for _, r := range p.AdvertiseRoutes {
				if r == "0.0.0.0/0" || r == "::/0" {
					advExit = true
				}
			}
			res["prefs"] = map[string]any{
				"accept_routes": p.RouteAll, "accept_dns": p.CorpDNS, "shields_up": p.ShieldsUp, "ssh": p.RunSSH,
				"exit_node_allow_lan_access": p.ExitNodeAllowLANAccess, "advertise_exit_node": advExit,
				"want_running": p.WantRunning, "logged_out": p.LoggedOut, "operator": p.OperatorUser,
			}
			if exitNode == nil && p.ExitNodeIP != "" {
				res["exit_node"] = map[string]any{"name": "", "ip": p.ExitNodeIP, "online": false}
			}
		}
	}
	serve := []map[string]any{}
	if out, err := tsRun(ctx, "serve", "status", "--json"); err == nil {
		serve = tsParseServe(out, selfDNS)
	}
	res["serve"] = serve
	return res
}

// tailscaleStatus is the cached answer; force asks the CLI again.
func (s *Server) tailscaleStatus(ctx context.Context, force bool) map[string]any {
	s.tsMu.Lock()
	if s.tsRes != nil && !force && time.Since(s.tsAt) < tsTTL {
		res := s.tsRes
		s.tsMu.Unlock()
		return res
	}
	s.tsMu.Unlock()
	res := tsCollect(ctx)
	s.tsMu.Lock()
	s.tsAt, s.tsRes = time.Now(), res
	s.tsMu.Unlock()
	return res
}

func isTailscaleIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	_, cgnat, _ := net.ParseCIDR("100.64.0.0/10")
	_, ula, _ := net.ParseCIDR("fd7a:115c:a1e0::/48")
	return cgnat.Contains(ip) || ula.Contains(ip)
}

// tsVia says how a request reached the daemon: "tailscale" (a tailnet
// address, or Tailscale Serve's proxy — it keeps the .ts.net Host and adds
// X-Forwarded-For), "local" (this machine) or "lan" (anything else).
func tsVia(r *http.Request) string {
	host := r.Host
	if h, _, err := net.SplitHostPort(r.Host); err == nil {
		host = h
	}
	if strings.HasSuffix(strings.ToLower(host), ".ts.net") {
		return "tailscale"
	}
	var ip net.IP
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		ip = net.ParseIP(h)
	} else {
		ip = net.ParseIP(r.RemoteAddr)
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if p := net.ParseIP(strings.TrimSpace(strings.Split(xff, ",")[0])); p != nil {
			ip = p
		}
	}
	switch {
	case isTailscaleIP(ip):
		return "tailscale"
	case ip == nil || ip.IsLoopback():
		return "local"
	default:
		return "lan"
	}
}

// tsDecorate adds the per-request facts to a copy of the cached answer.
func (s *Server) tsDecorate(res map[string]any, r *http.Request) map[string]any {
	out := make(map[string]any, len(res)+2)
	for k, v := range res {
		out[k] = v
	}
	out["via"] = tsVia(r)
	listenTS := false
	if c := s.cfg.Load(); c != nil {
		if h, _, err := net.SplitHostPort(c.Listen); err == nil {
			listenTS = isTailscaleIP(net.ParseIP(h))
		}
	}
	out["listen_on_tailscale"] = listenTS
	return out
}

// GET /v1/tailscale[?force=1] — the module's status. The "detected" and
// "ip" keys predate the module (the Daemon settings' Bind to Tailscale IP
// button reads them) and stay.
func (s *Server) handleTailscale(w http.ResponseWriter, r *http.Request) {
	res := s.tailscaleStatus(r.Context(), r.URL.Query().Get("force") != "")
	writeJSON(w, http.StatusOK, s.tsDecorate(res, r))
}

// POST /v1/tailscale/set {action: up|down|login|set, key, value} — one
// change through the CLI, then the fresh status. up waits at most 15 s for
// Running; login only asks tailscaled to start a login and lets the status
// carry the link; set takes a bool for a tsBoolFlags key or a device (IP
// or name) offering an exit node — or "" — for exit_node. --accept-risk
// stands in for the CLI's terminal prompt: the desktop has already asked.
func (s *Server) handleTailscaleSet(w http.ResponseWriter, r *http.Request) {
	if tsBinary() == "" {
		writeErr(w, http.StatusNotFound, errors.New("tailscale is not installed on this machine"))
		return
	}
	var req struct {
		Action string          `json:"action"`
		Key    string          `json:"key"`
		Value  json.RawMessage `json:"value"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("bad request: %w", err))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	var args []string
	switch req.Action {
	case "up":
		args = []string{"up", "--timeout=15s"}
	case "down":
		args = []string{"down", "--accept-risk=all"}
	case "login":
		args = []string{"up", "--timeout=3s"}
	case "set":
		if flag, ok := tsBoolFlags[req.Key]; ok {
			var v bool
			if json.Unmarshal(req.Value, &v) != nil {
				writeErr(w, http.StatusBadRequest, fmt.Errorf("%s wants true or false", req.Key))
				return
			}
			args = []string{"set", flag + "=" + strconv.FormatBool(v), "--accept-risk=all"}
			break
		}
		if req.Key != "exit_node" {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("unknown setting %q", req.Key))
			return
		}
		var v string
		if json.Unmarshal(req.Value, &v) != nil {
			writeErr(w, http.StatusBadRequest, errors.New("exit_node wants a device name or IP, or \"\""))
			return
		}
		v = strings.TrimSpace(v)
		if v != "" && !s.tsOffersExit(ctx, v) {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("%q is not a device that offers an exit node", v))
			return
		}
		args = []string{"set", "--exit-node=" + v, "--accept-risk=all"}
	default:
		writeErr(w, http.StatusBadRequest, fmt.Errorf("unknown action %q", req.Action))
		return
	}
	if _, err := tsRun(ctx, args...); err != nil && req.Action != "login" {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	res := s.tailscaleStatus(ctx, true)
	writeJSON(w, http.StatusOK, s.tsDecorate(res, r))
}

// tsOffersExit says whether v names (by IP or tailnet name) a device that
// offers an exit node, per a fresh status.
func (s *Server) tsOffersExit(ctx context.Context, v string) bool {
	res := s.tailscaleStatus(ctx, true)
	peers, _ := res["peers"].([]map[string]any)
	for _, p := range peers {
		if opt, _ := p["exit_node_option"].(bool); !opt {
			continue
		}
		if strings.EqualFold(fmt.Sprint(p["name"]), v) || fmt.Sprint(p["ip"]) == v || strings.EqualFold(fmt.Sprint(p["dns_name"]), v) {
			return true
		}
	}
	return false
}
