//go:build e2e_fleet

package authcapabilitywschaos

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/url"
	"sync"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/edge"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

// The capability upgrade bucket: 60 a minute per address, burst 20, on the
// gateway that sees the client (core/pkg/gateway/ws_capability.go
// capabilityUpgradeBurst, rate_limiter.go).
const (
	upgradeBurst  = 20
	floodUpgrades = 2 * upgradeBurst
	// forgedBytes is the random payload of the capability the flood presents.
	forgedBytes = 48
	// noSuchFunction is judged like any other capability: the same 403
	// (docs/AUTH.md#capability-websockets), so the flood needs no deployment.
	noSuchFunction = "e2e-no-such-fn"
)

// TestCapabilityUpgrades_limitedPerAddress: past the burst, capability
// upgrades from one address to one gateway are refused with 429 before they
// are judged; the ones judged are the capability's 403. The upgrades are
// sent at once: the bucket refills a token a second, and a handshake takes
// 0.2-1.3s over the edge, so a serial flood of 25 refills about as many tokens
// as it spends and is never limited.
func TestCapabilityUpgrades_limitedPerAddress(t *testing.T) {
	f := harness.Fleet(t)
	quiesce(t, f)
	c := harness.GW(t).Unpaced().PinTo(f.State.Nodes[0].PublicIP)
	path := "/v1/functions/" + noSuchFunction + "/ws?" +
		url.Values{"namespace": {gw.LobbyNamespace}, "cap": {forgedCapability(t)}}.Encode()
	statuses := make([]int, floodUpgrades)
	opened := make([]bool, floodUpgrades)
	var wg sync.WaitGroup
	for i := range floodUpgrades {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, resp, _ := c.DialWS(t.Context(), path, "", nil)
			if conn != nil {
				conn.Close()
				opened[i] = true
			}
			if resp != nil {
				statuses[i] = resp.StatusCode
			}
		}()
	}
	wg.Wait()
	limited := 0
	for i, status := range statuses {
		switch {
		case opened[i]:
			t.Fatalf("upgrade %d: a forged capability opened a socket", i+1)
		case status == http.StatusForbidden:
		case status == http.StatusTooManyRequests:
			limited++
		case status == 0:
			t.Fatalf("upgrade %d: no handshake answer", i+1)
		default:
			t.Errorf("upgrade %d answered %d", i+1, status)
		}
	}
	if limited == 0 {
		t.Fatalf("%d capability upgrades from one address at once were all judged; the burst is %d", floodUpgrades, upgradeBurst)
	}
}

// forgedCapability is random bytes, never derived from a real capability.
func forgedCapability(t *testing.T) string {
	t.Helper()
	b := make([]byte, forgedBytes)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("failed to read random bytes for a forged capability: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// quiesce holds the run's credential pacer before the flood and again, from
// a cleanup, after it (edge.Quiesce): the flood runs with no paced traffic of
// this address in flight and leaves the gateway a refill period to recover.
func quiesce(t *testing.T, f *fleet.Fleet) {
	t.Helper()
	if err := edge.Quiesce(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := fleet.CleanupContext(t)
		defer cancel()
		if err := edge.Quiesce(ctx); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	})
}
