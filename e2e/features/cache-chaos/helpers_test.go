//go:build e2e_fleet

package cachechaos

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

const (
	pathGet    = "/v1/cache/get"
	pathPut    = "/v1/cache/put"
	pathHealth = "/v1/cache/health"
	// pollEvery paces every wait in this package.
	pollEvery = 2 * time.Second
	// reconcileBudget: the tenant reconciler starts a missing service every
	// 60s (website/src/docs/contributor/architecture-reference.mdx "The tenant plane converges"); two sweeps and
	// a start.
	reconcileBudget = 3 * time.Minute
	// dropBudget: the gateway probes Olric every 10s, each probe bounded by
	// 10s, and drops the client after three failures (website/src/docs/contributor/architecture-reference.mdx
	// "Olric is supervised, not connected once"); plus a request.
	dropBudget = 90 * time.Second
	// reconnectBudget: reconnect backoff is capped at 30s, plus a probe.
	reconnectBudget = 2 * time.Minute
)

// cachePut writes key=value through c. A request that could not be made is
// an error, not a failure: inside a wait it is the current observation.
func cachePut(t testing.TB, c *gw.Client, n *ns.Namespace, key string, value any) (*gw.Response, error) {
	t.Helper()
	return cacheCall(t, c, n, pathPut, map[string]any{"dmap": "chaos", "key": key, "value": value})
}

func cacheGet(t testing.TB, c *gw.Client, n *ns.Namespace, key string) (*gw.Response, error) {
	t.Helper()
	return cacheCall(t, c, n, pathGet, map[string]any{"dmap": "chaos", "key": key})
}

func cacheCall(t testing.TB, c *gw.Client, n *ns.Namespace, path string, body any) (*gw.Response, error) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to encode the %s body: %w", path, err)
	}
	return c.For(t).Send(t.Context(), gw.Req{Method: http.MethodPost, Path: path, Bearer: n.Owner.Token(),
		Header: http.Header{"Content-Type": {"application/json"}}, Body: raw})
}

// wantStatus is an eventually probe for "the call answered status"; a call
// that could not be made is the current observation, not a failure.
func wantStatus(resp *gw.Response, err error, status int) (bool, error) {
	if err != nil {
		return false, err
	}
	if resp.Status != status {
		return false, fmt.Errorf("HTTP %d: %.200s", resp.Status, resp.Body)
	}
	return true, nil
}

// mustAnswer fails the test when the call could not be made at all.
func mustAnswer(t testing.TB, resp *gw.Response, err error) *gw.Response {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	return resp
}
