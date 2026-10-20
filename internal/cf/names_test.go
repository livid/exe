package cf

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

// The health check names what it checked: the zone and its account from
// the zone itself, or the account from the token's list of accounts.
func TestZoneAndAccountName(t *testing.T) {
	answer := map[string]string{
		"/client/v4/zones/z1": `{"id":"z1","name":"example.org","account":{"id":"a1","name":"Example Team"}}`,
		"/client/v4/accounts": `[{"id":"a0","name":"Other"},{"id":"a1","name":"Example Team"}]`,
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
	name, acct, err := (&Client{Token: "t", ZoneID: "z1", HTTPC: httpc}).Zone(ctx)
	if err != nil || name != "example.org" || acct.ID != "a1" || acct.Name != "Example Team" {
		t.Errorf("zone z1: %q %+v %v", name, acct, err)
	}
	if _, _, err := (&Client{Token: "t", ZoneID: "z2", HTTPC: httpc}).Zone(ctx); err == nil {
		t.Errorf("a zone the token cannot read passed")
	}
	for _, tc := range []struct{ account, want string }{{"a1", "Example Team"}, {"a9", ""}} {
		c := &Client{Token: "t", AccountID: tc.account, HTTPC: httpc}
		if got := c.AccountName(ctx); got != tc.want {
			t.Errorf("account %s: %q, want %q", tc.account, got, tc.want)
		}
	}
}
