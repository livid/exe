package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"strings"
	"time"

	"exe/internal/cf"
)

const cfHealthTTL = 60 * time.Second

// handleCFHealth is the Cloudflare heartbeat: it verifies the token, DNS
// access, and the tunnel (including whether cloudflared is connected).
// Results are cached for cfHealthTTL; ?force=1 bypasses the cache, and a
// config change invalidates it via the key.
func (s *Server) handleCFHealth(w http.ResponseWriter, r *http.Request) {
	c := s.Config().Cloudflare
	key := strings.Join([]string{c.APIToken, c.AccountID, c.ZoneID, c.TunnelID, c.Domain}, "|")
	force := r.URL.Query().Get("force") != ""

	s.cfHealthMu.Lock()
	if !force && s.cfHealthRes != nil && s.cfHealthKey == key && time.Since(s.cfHealthAt) < cfHealthTTL {
		res := s.cfHealthRes
		s.cfHealthMu.Unlock()
		writeJSON(w, http.StatusOK, res)
		return
	}
	s.cfHealthMu.Unlock()

	res := s.checkCFHealth(r.Context())
	res["checked_at"] = time.Now().UTC()
	s.cfHealthMu.Lock()
	s.cfHealthAt, s.cfHealthKey, s.cfHealthRes = time.Now(), key, res
	s.cfHealthMu.Unlock()
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) checkCFHealth(ctx context.Context) map[string]any {
	res := s.cfProbe(ctx)
	if res["status"] == "error" {
		// Cloudflared connectors cycle and the CF API has blips; a single
		// failed probe shouldn't flip the dot orange for a whole cache TTL.
		select {
		case <-ctx.Done():
			return res
		case <-time.After(2 * time.Second):
		}
		res = s.cfProbe(ctx)
	}
	return res
}

func (s *Server) cfProbe(ctx context.Context) map[string]any {
	c := s.Config().Cloudflare
	if c.APIToken == "" || c.AccountID == "" || c.ZoneID == "" || c.TunnelID == "" || c.Domain == "" {
		return map[string]any{
			"status":  "unconfigured",
			"message": "Cloudflare is not fully configured — click to run the setup wizard.",
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	client := &cf.Client{Token: c.APIToken, AccountID: c.AccountID, ZoneID: c.ZoneID, TunnelID: c.TunnelID, Domain: c.Domain}

	var problems, notes []string
	// the names beside the IDs, for Configuration → Cloudflare to show
	names := map[string]any{}
	if err := client.VerifyToken(ctx); err != nil {
		problems = append(problems, "token: "+err.Error())
	} else {
		if zone, err := client.DNSAccess(ctx); err != nil {
			problems = append(problems, "zone DNS access: "+err.Error())
		} else if zone != "" {
			names["zone_name"] = zone
		}
		if acct := client.AccountName(ctx); acct != "" {
			names["account_name"] = acct
		}
		if t, err := client.GetTunnel(ctx, c.TunnelID); err != nil {
			problems = append(problems, "tunnel: "+err.Error())
		} else {
			names["tunnel_name"] = t.Name
			switch t.Status {
			case "healthy":
			case "degraded":
				notes = append(notes, fmt.Sprintf("tunnel %q is degraded", t.Name))
			default: // inactive, down
				problems = append(problems, fmt.Sprintf("tunnel %q is %s (cloudflared not connected?)", t.Name, t.Status))
			}
			if !t.RemoteConfig {
				notes = append(notes, "tunnel is locally managed; exe cannot push ingress rules")
			}
		}
	}
	if len(problems) > 0 {
		res := map[string]any{
			"status":  "error",
			"message": "Cloudflare problem: " + strings.Join(problems, "; ") + " — click to run the setup wizard.",
		}
		maps.Copy(res, names)
		return res
	}
	msg := "Cloudflare is working: token, DNS access, and tunnel all check out."
	if len(notes) > 0 {
		msg += " (" + strings.Join(notes, "; ") + ")"
	}
	res := map[string]any{"status": "ok", "message": msg}
	maps.Copy(res, names)
	return res
}

// handleCFWizard validates one step of the Cloudflare setup wizard.
// Validation failures are ok:false with a message (HTTP 200); HTTP errors
// are reserved for malformed requests. Successful steps also return the
// pick-lists the next step can offer (accounts, zones, tunnels).
func (s *Server) handleCFWizard(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Step      string `json:"step"`
		APIToken  string `json:"api_token"`
		AccountID string `json:"account_id"`
		ZoneID    string `json:"zone_id"`
		TunnelID  string `json:"tunnel_id"`
		Domain    string `json:"domain"`
		ZoneName  string `json:"zone_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	client := &cf.Client{
		Token:     strings.TrimSpace(req.APIToken),
		AccountID: strings.TrimSpace(req.AccountID),
		ZoneID:    strings.TrimSpace(req.ZoneID),
		TunnelID:  strings.TrimSpace(req.TunnelID),
	}
	res := map[string]any{"ok": false}
	fail := func(msg string) {
		res["message"] = msg
		writeJSON(w, http.StatusOK, res)
	}

	switch req.Step {
	case "api_token":
		if client.Token == "" {
			fail("Paste the API token first.")
			return
		}
		if err := client.VerifyToken(ctx); err != nil {
			fail("Cloudflare rejected the token: " + err.Error())
			return
		}
		res["ok"] = true
		res["message"] = "Token is valid and active."
		if accounts, err := client.ListAccounts(ctx); err == nil && len(accounts) > 0 {
			res["accounts"] = accounts
		}

	case "account_id":
		if client.AccountID == "" {
			fail("Provide the account ID.")
			return
		}
		tunnels, err := client.ListTunnels(ctx)
		if err != nil {
			fail("Cannot access Cloudflare Tunnels on this account — the token needs Account → Cloudflare Tunnel → Edit. (" + err.Error() + ")")
			return
		}
		res["ok"] = true
		res["message"] = fmt.Sprintf("Account is accessible; found %d tunnel(s).", len(tunnels))
		res["tunnels"] = tunnels
		if zones, err := client.ListZones(ctx); err == nil && len(zones) > 0 {
			res["zones"] = zones
		}

	case "zone_id":
		if client.ZoneID == "" {
			fail("Provide the zone ID.")
			return
		}
		if err := client.CheckDNSAccess(ctx); err != nil {
			fail("Cannot read DNS records in this zone — the token needs Zone → DNS → Edit. (" + err.Error() + ")")
			return
		}
		name, _ := client.ZoneName(ctx)
		res["ok"] = true
		res["zone_name"] = name
		if name != "" {
			res["message"] = "DNS access confirmed for zone " + name + "."
		} else {
			res["message"] = "DNS access confirmed."
		}

	case "tunnel_id":
		if client.AccountID == "" || client.TunnelID == "" {
			fail("Provide the tunnel ID.")
			return
		}
		t, err := client.GetTunnel(ctx, client.TunnelID)
		if err != nil {
			fail("Tunnel not found in this account: " + err.Error())
			return
		}
		res["ok"] = true
		res["message"] = fmt.Sprintf("Tunnel %q found (status: %s).", t.Name, t.Status)
		if !t.RemoteConfig {
			res["warning"] = "This tunnel is locally managed (config file on the cloudflared host), so exe cannot push ingress rules to it. Either add a catch-all ingress to the exe proxy manually, or recreate the tunnel from the Zero Trust dashboard to make it remotely managed."
		}

	case "domain":
		d := strings.ToLower(strings.TrimSpace(req.Domain))
		if d == "" || strings.ContainsAny(d, "/: ") || !strings.Contains(d, ".") {
			fail("Enter a bare domain like example.com or apps.example.com.")
			return
		}
		zn := strings.ToLower(strings.TrimSpace(req.ZoneName))
		if zn != "" && d != zn && !strings.HasSuffix(d, "."+zn) {
			fail(fmt.Sprintf("%s is not inside zone %s.", d, zn))
			return
		}
		res["ok"] = true
		res["message"] = "Looks good — apps will publish as <name>." + d + "."

	default:
		writeErr(w, http.StatusBadRequest, errors.New("unknown wizard step"))
		return
	}
	writeJSON(w, http.StatusOK, res)
}
