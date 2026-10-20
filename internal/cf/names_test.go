package cf

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

// The health check names what it checked: the zone from the DNS record it
// reads anyway, the account from the token's list of accounts.
func TestDNSAccessAndAccountName(t *testing.T) {
	answer := map[string]string{
		"/client/v4/zones/z1/dns_records": `[{"id":"r","zone_name":"example.org"}]`,
		"/client/v4/zones/z2/dns_records": `[]`,
		"/client/v4/accounts":             `[{"id":"a0","name":"Other"},{"id":"a1","name":"Example Team"}]`,
	}
	httpc := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		result, ok := answer[r.URL.Path]
		body := `{"success":true,"errors":[],"result":` + result + `}`
		if !ok {
			body = `{"success":false,"errors":[{"code":10000,"message":"Authentication error"}],"result":null}`
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	ctx := context.Background()
	for _, tc := range []struct{ zone, want string }{{"z1", "example.org"}, {"z2", ""}} {
		c := &Client{Token: "t", ZoneID: tc.zone, HTTPC: httpc}
		if got, err := c.DNSAccess(ctx); err != nil || got != tc.want {
			t.Errorf("zone %s: %q, %v; want %q", tc.zone, got, err, tc.want)
		}
	}
	if _, err := (&Client{Token: "t", ZoneID: "z3", HTTPC: httpc}).DNSAccess(ctx); err == nil {
		t.Errorf("a zone the token cannot read passed")
	}
	for _, tc := range []struct{ account, want string }{{"a1", "Example Team"}, {"a9", ""}} {
		c := &Client{Token: "t", AccountID: tc.account, HTTPC: httpc}
		if got := c.AccountName(ctx); got != tc.want {
			t.Errorf("account %s: %q, want %q", tc.account, got, tc.want)
		}
	}
}
