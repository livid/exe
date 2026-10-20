// Package cf talks to the Cloudflare v4 API to publish VM services through a
// remotely-managed Cloudflare Tunnel: a CNAME per hostname plus a tunnel
// ingress rule pointing back at this machine's reverse proxy.
package cf

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const apiBase = "https://api.cloudflare.com/client/v4"

type Client struct {
	Token     string
	AccountID string
	ZoneID    string
	TunnelID  string
	Domain    string
	HTTPC     *http.Client
}

func (c *Client) Configured() bool {
	return c.Token != "" && c.AccountID != "" && c.ZoneID != "" && c.TunnelID != "" && c.Domain != ""
}

func (c *Client) httpc() *http.Client {
	if c.HTTPC != nil {
		return c.HTTPC
	}
	return &http.Client{Timeout: 30 * time.Second}
}

type apiEnvelope struct {
	Success bool `json:"success"`
	Errors  []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
	Result json.RawMessage `json:"result"`
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, apiBase+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpc().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var env apiEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("cloudflare %s %s: HTTP %d: %.500s", method, path, resp.StatusCode, raw)
	}
	if !env.Success {
		var msgs []string
		for _, e := range env.Errors {
			msgs = append(msgs, fmt.Sprintf("%d: %s", e.Code, e.Message))
		}
		return fmt.Errorf("cloudflare %s %s failed: %s", method, path, strings.Join(msgs, "; "))
	}
	if out != nil && len(env.Result) > 0 {
		return json.Unmarshal(env.Result, out)
	}
	return nil
}

type IDName struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Tunnel struct {
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	Status       string       `json:"status"`
	RemoteConfig bool         `json:"remote_config"`
	Connections  []Connection `json:"connections"`
}

type Connection struct {
	ID               string `json:"id"`
	ClientID         string `json:"client_id"`
	Location         string `json:"colo_name"`
	PendingReconnect bool   `json:"is_pending_reconnect"`
}

// VerifyToken checks that the API token itself is valid and active.
func (c *Client) VerifyToken(ctx context.Context) error {
	var res struct {
		Status string `json:"status"`
	}
	if err := c.do(ctx, "GET", "/user/tokens/verify", nil, &res); err != nil {
		return err
	}
	if res.Status != "active" {
		return fmt.Errorf("token status is %q, expected active", res.Status)
	}
	return nil
}

func (c *Client) ListAccounts(ctx context.Context) ([]IDName, error) {
	var out []IDName
	err := c.do(ctx, "GET", "/accounts?per_page=50", nil, &out)
	return out, err
}

func (c *Client) ListZones(ctx context.Context) ([]IDName, error) {
	var out []IDName
	err := c.do(ctx, "GET", "/zones?per_page=50", nil, &out)
	return out, err
}

// ZoneFor returns the ID of the zone the token holds that fqdn falls in —
// the longest suffix of fqdn naming one — or "" when it holds none.
func (c *Client) ZoneFor(ctx context.Context, fqdn string) (string, error) {
	labels := strings.Split(strings.TrimSuffix(fqdn, "."), ".")
	for i := 0; i+1 < len(labels); i++ {
		var out []IDName
		q := url.Values{"name": {strings.Join(labels[i:], ".")}}
		if err := c.do(ctx, "GET", "/zones?"+q.Encode(), nil, &out); err != nil {
			return "", err
		}
		if len(out) > 0 {
			return out[0].ID, nil
		}
	}
	return "", nil
}

func (c *Client) ListTunnels(ctx context.Context) ([]Tunnel, error) {
	var out []Tunnel
	err := c.do(ctx, "GET", "/accounts/"+c.AccountID+"/cfd_tunnel?is_deleted=false&per_page=50", nil, &out)
	return out, err
}

func (c *Client) GetTunnel(ctx context.Context, id string) (*Tunnel, error) {
	var out Tunnel
	if err := c.do(ctx, "GET", "/accounts/"+c.AccountID+"/cfd_tunnel/"+id, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CheckDNSAccess verifies the token can read DNS records in the zone — the
// exact capability EnsureDNS needs (DNS Edit implies read).
func (c *Client) CheckDNSAccess(ctx context.Context) error {
	var out []json.RawMessage
	return c.do(ctx, "GET", "/zones/"+c.ZoneID+"/dns_records?per_page=1", nil, &out)
}

// Zone is the client's zone as Cloudflare names it, and the account that
// holds it. (A DNS record no longer carries its zone's name.)
func (c *Client) Zone(ctx context.Context) (name string, account IDName, err error) {
	var z struct {
		Name    string `json:"name"`
		Account IDName `json:"account"`
	}
	if err := c.do(ctx, "GET", "/zones/"+c.ZoneID, nil, &z); err != nil {
		return "", IDName{}, err
	}
	return z.Name, z.Account, nil
}

// AccountName is the name of the client's account among those the token
// lists, or "" when it lists none by that ID.
func (c *Client) AccountName(ctx context.Context) string {
	accounts, err := c.ListAccounts(ctx)
	if err != nil {
		return ""
	}
	for _, a := range accounts {
		if a.ID == c.AccountID {
			return a.Name
		}
	}
	return ""
}

// ZoneName resolves the zone's domain name. Tokens without Zone Read fall
// back to the zone_name field of an existing DNS record; empty if neither
// route works.
func (c *Client) ZoneName(ctx context.Context) (string, error) {
	var z IDName
	if err := c.do(ctx, "GET", "/zones/"+c.ZoneID, nil, &z); err == nil && z.Name != "" {
		return z.Name, nil
	}
	var recs []struct {
		ZoneName string `json:"zone_name"`
	}
	if err := c.do(ctx, "GET", "/zones/"+c.ZoneID+"/dns_records?per_page=1", nil, &recs); err != nil {
		return "", err
	}
	if len(recs) > 0 {
		return recs[0].ZoneName, nil
	}
	return "", nil
}

// DeleteDNS removes the CNAME record(s) for fqdn, if any.
func (c *Client) DeleteDNS(ctx context.Context, fqdn string) error {
	return c.deleteRecords(ctx, "CNAME", fqdn)
}

// DeleteTXT removes the TXT record(s) at fqdn, if any.
func (c *Client) DeleteTXT(ctx context.Context, fqdn string) error {
	return c.deleteRecords(ctx, "TXT", fqdn)
}

func (c *Client) deleteRecords(ctx context.Context, typ, fqdn string) error {
	q := url.Values{"type": {typ}, "name": {fqdn}}
	var existing []struct {
		ID string `json:"id"`
	}
	if err := c.do(ctx, "GET", "/zones/"+c.ZoneID+"/dns_records?"+q.Encode(), nil, &existing); err != nil {
		return err
	}
	for _, rec := range existing {
		if err := c.do(ctx, "DELETE", "/zones/"+c.ZoneID+"/dns_records/"+rec.ID, nil, nil); err != nil {
			return err
		}
	}
	return nil
}

// SetTXT makes fqdn hold one TXT record whose single string is text: the
// first record there is rewritten and any others deleted, so a name such
// as _dnslink.<host> never answers two values. The content goes in
// quotation marks, the form Cloudflare asks for.
func (c *Client) SetTXT(ctx context.Context, fqdn, text string, ttl int) error {
	q := url.Values{"type": {"TXT"}, "name": {fqdn}}
	var existing []struct {
		ID string `json:"id"`
	}
	if err := c.do(ctx, "GET", "/zones/"+c.ZoneID+"/dns_records?"+q.Encode(), nil, &existing); err != nil {
		return err
	}
	rec := map[string]any{"type": "TXT", "name": fqdn, "content": `"` + text + `"`, "ttl": ttl}
	if len(existing) == 0 {
		return c.do(ctx, "POST", "/zones/"+c.ZoneID+"/dns_records", rec, nil)
	}
	if err := c.do(ctx, "PUT", "/zones/"+c.ZoneID+"/dns_records/"+existing[0].ID, rec, nil); err != nil {
		return err
	}
	for _, extra := range existing[1:] {
		if err := c.do(ctx, "DELETE", "/zones/"+c.ZoneID+"/dns_records/"+extra.ID, nil, nil); err != nil {
			return err
		}
	}
	return nil
}

// RemoveIngress drops the tunnel ingress rule for fqdn, preserving the rest.
func (c *Client) RemoveIngress(ctx context.Context, fqdn string) error {
	var got struct {
		Config map[string]any `json:"config"`
	}
	path := "/accounts/" + c.AccountID + "/cfd_tunnel/" + c.TunnelID + "/configurations"
	if err := c.do(ctx, "GET", path, nil, &got); err != nil {
		return err
	}
	if got.Config == nil {
		return nil
	}
	rules, ok := got.Config["ingress"].([]any)
	if !ok {
		return nil
	}
	kept := make([]any, 0, len(rules))
	for _, r := range rules {
		if m, isMap := r.(map[string]any); isMap {
			if h, _ := m["hostname"].(string); h == fqdn {
				continue
			}
		}
		kept = append(kept, r)
	}
	if len(kept) == len(rules) {
		return nil
	}
	got.Config["ingress"] = kept
	return c.do(ctx, "PUT", path, map[string]any{"config": got.Config}, nil)
}

// EnsureDNS upserts a proxied CNAME fqdn -> <tunnel>.cfargotunnel.com.
func (c *Client) EnsureDNS(ctx context.Context, fqdn string) error {
	content := c.TunnelID + ".cfargotunnel.com"
	q := url.Values{"type": {"CNAME"}, "name": {fqdn}}
	var existing []struct {
		ID string `json:"id"`
	}
	if err := c.do(ctx, "GET", "/zones/"+c.ZoneID+"/dns_records?"+q.Encode(), nil, &existing); err != nil {
		return err
	}
	rec := map[string]any{"type": "CNAME", "name": fqdn, "content": content, "proxied": true, "ttl": 1}
	if len(existing) > 0 {
		return c.do(ctx, "PUT", "/zones/"+c.ZoneID+"/dns_records/"+existing[0].ID, rec, nil)
	}
	return c.do(ctx, "POST", "/zones/"+c.ZoneID+"/dns_records", rec, nil)
}

// SyncIngress makes every hostname in services route to its service value:
// existing rules are rewritten, hostnames without a rule get one appended
// before the catch-all. One read-modify-write, so rules for other apps
// sharing the tunnel are untouched. Returns the hostnames whose rules
// actually changed; no PUT is issued when everything already matches.
func (c *Client) SyncIngress(ctx context.Context, services map[string]string) ([]string, error) {
	if len(services) == 0 {
		return nil, nil
	}
	var got struct {
		Config map[string]any `json:"config"`
	}
	path := "/accounts/" + c.AccountID + "/cfd_tunnel/" + c.TunnelID + "/configurations"
	if err := c.do(ctx, "GET", path, nil, &got); err != nil {
		return nil, err
	}
	cfg := got.Config
	if cfg == nil {
		cfg = map[string]any{}
	}
	var rules []any
	if r, ok := cfg["ingress"].([]any); ok {
		rules = r
	}

	var changed []string
	missing := make(map[string]string, len(services))
	for h, svc := range services {
		missing[h] = svc
	}
	for i, r := range rules {
		m, ok := r.(map[string]any)
		if !ok {
			continue
		}
		h, _ := m["hostname"].(string)
		svc, want := services[h]
		if !want {
			continue
		}
		delete(missing, h)
		if cur, _ := m["service"].(string); cur != svc {
			m["service"] = svc
			rules[i] = m
			changed = append(changed, h)
		}
	}
	if len(missing) > 0 {
		catchAll := map[string]any{"service": "http_status:404"}
		if n := len(rules); n > 0 {
			if m, ok := rules[n-1].(map[string]any); ok {
				if h, _ := m["hostname"].(string); h == "" {
					catchAll = m
					rules = rules[:n-1]
				}
			}
		}
		for h, svc := range missing {
			rules = append(rules, map[string]any{"hostname": h, "service": svc})
			changed = append(changed, h)
		}
		rules = append(rules, catchAll)
	}
	if len(changed) == 0 {
		return nil, nil
	}
	sort.Strings(changed)
	cfg["ingress"] = rules
	if err := c.do(ctx, "PUT", path, map[string]any{"config": cfg}, nil); err != nil {
		return nil, err
	}
	return changed, nil
}

// EnsureIngress upserts an ingress rule hostname->service in the tunnel's
// remotely-managed configuration, preserving other rules and keeping a
// catch-all rule last. Locally-managed tunnels (config.yml on the
// cloudflared host) cannot be configured this way.
func (c *Client) EnsureIngress(ctx context.Context, fqdn, service string) error {
	var got struct {
		Config map[string]any `json:"config"`
	}
	path := "/accounts/" + c.AccountID + "/cfd_tunnel/" + c.TunnelID + "/configurations"
	if err := c.do(ctx, "GET", path, nil, &got); err != nil {
		return err
	}
	cfg := got.Config
	if cfg == nil {
		cfg = map[string]any{}
	}
	var rules []any
	if r, ok := cfg["ingress"].([]any); ok {
		rules = r
	}

	updated := false
	for i, r := range rules {
		m, ok := r.(map[string]any)
		if !ok {
			continue
		}
		if h, _ := m["hostname"].(string); h == fqdn {
			m["service"] = service
			rules[i] = m
			updated = true
			break
		}
	}
	if !updated {
		newRule := map[string]any{"hostname": fqdn, "service": service}
		catchAll := map[string]any{"service": "http_status:404"}
		if n := len(rules); n > 0 {
			if m, ok := rules[n-1].(map[string]any); ok {
				if h, _ := m["hostname"].(string); h == "" {
					catchAll = m
					rules = rules[:n-1]
				}
			}
		}
		rules = append(rules, newRule, catchAll)
	}
	cfg["ingress"] = rules
	return c.do(ctx, "PUT", path, map[string]any{"config": cfg}, nil)
}
