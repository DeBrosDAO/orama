//go:build e2e_fleet

package smoke

import (
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

// publicHealthKeys are the only members an anonymous /health may carry
// (docs/API_SURFACE.md): the detail is /v1/operator/health's.
var publicHealthKeys = []string{"checks", "server", "status"}

func TestHealth_publicShapeOnly(t *testing.T) {
	t.Parallel()
	c := harness.GW(t)
	for _, path := range []string{"/health", "/v1/health"} {
		resp := c.MustSend(t, gw.Req{Path: path}).Expect(t, http.StatusOK)
		var body map[string]any
		if err := resp.Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["status"] != "healthy" {
			t.Errorf("%s: status %v, want healthy", path, body["status"])
		}
		var keys []string
		for k := range body {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if strings.Join(keys, ",") != strings.Join(publicHealthKeys, ",") {
			t.Errorf("%s: anonymous health exposes %v, want only %v", path, keys, publicHealthKeys)
		}
	}
}

func TestHealth_hostileQueryNotReflected(t *testing.T) {
	t.Parallel()
	c := harness.GW(t)
	resp := c.MustSend(t, gw.Req{Path: "/health", Query: map[string][]string{"x": {strings.Repeat("‮<script>", 200)}}})
	if resp.Status >= http.StatusInternalServerError {
		t.Errorf("hostile query string broke /health: %d", resp.Status)
	}
	if strings.Contains(string(resp.Body), "<script>") {
		t.Error("/health reflected the query string")
	}
}
