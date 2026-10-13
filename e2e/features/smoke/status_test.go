//go:build e2e_fleet

package smoke

import (
	"net/http"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

// TestStatus_leaksNoNodeAddress checks the open status route against every
// address the run knows: /v1/status is public and must not map the fleet.
func TestStatus_leaksNoNodeAddress(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	c := harness.GW(t)
	resp := c.MustSend(t, gw.Req{Path: "/v1/status", Header: http.Header{"Accept": {"application/json"}}}).Expect(t, http.StatusOK)
	var body map[string]any
	if err := resp.Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["status"] == nil || body["server"] == nil {
		t.Fatalf("status lacks status/server: %s", resp.Body)
	}
	text := string(resp.Body)
	for _, n := range f.AllNodes() {
		for _, addr := range []string{n.PublicIP, n.WGIP} {
			if addr != "" && strings.Contains(text, addr) {
				t.Errorf("/v1/status exposes %s's address %s", n.Name, addr)
			}
		}
	}
}

func TestStatus_browserGetsHTMLPage(t *testing.T) {
	t.Parallel()
	c := harness.GW(t)
	resp := c.MustSend(t, gw.Req{Path: "/status", Header: http.Header{"Accept": {"text/html"}}}).Expect(t, http.StatusOK)
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("content type %q, want text/html", ct)
	}
}
