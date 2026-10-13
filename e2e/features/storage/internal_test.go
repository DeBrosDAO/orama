//go:build e2e_fleet

package storage

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

const (
	pathEvict     = "/v1/internal/storage/evict"
	pathReencrypt = "/v1/internal/secrets/reencrypt"
	// gatewayPort is the index gateway on every node (core constants
	// GatewayAPIPort), where node-to-node /v1/internal calls land.
	gatewayPort = 10104
	// evictMarker is the header value the eviction fan-out sends
	// (core/pkg/gateway/handlers/storage/evict_handler.go).
	evictMarker = "storage-coordination"
)

// TestInternal_notReachableByClients: the node-to-node routes answer a client
// on the public name with a refusal (docs/whitepaper/technical-reference/appendices/i-api-surface.md: "Never reachable by
// a client").
func TestInternal_notReachableByClients(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	c := harness.GW(t)
	// The evict handler refuses a request that is not from a WireGuard peer
	// with the marker (evict_handler.go, 403); reencrypt refuses one without
	// the coordination MAC (secrets_rotate.go handleInternalReencrypt, 401).
	want := map[string]int{pathEvict: http.StatusForbidden, pathReencrypt: http.StatusUnauthorized}
	for path, status := range want {
		for name, who := range map[string]tenancy.Cred{"anonymous": {}, "namespace owner": tenancy.Owner(n)} {
			r := tenancy.Post(t, c, path, who, map[string]string{"cid": "QmYwAPJzv5CZsnA625s3Xf2nemtYgPpHdWEz79ojWnPbdG", "root": "x"})
			if r.Status != status {
				t.Errorf("%s %s: want %d, got %d %.200s", name, path, status, r.Status, r.Body)
			}
		}
	}
}

// TestInternal_evictNeedsMoreThanTheOverlay: a local process on one node,
// running as an unprivileged user, must not be able to evict another
// namespace's content from a peer's blockstore by sending the marker header
// over WireGuard. The handler checks only the WireGuard source address and
// the marker (evict_handler.go), so this asserts the stronger property the
// other node-to-node routes have (a coordination MAC).
func TestInternal_evictNeedsMoreThanTheOverlay(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{})
	data := randomBytes(t, smallBytes)
	u := upload(t, n.Client, tenancy.Owner(n), "victim.bin", data)
	waitPinned(t, n.Client, tenancy.Owner(n), u.Cid)
	from, to := f.State.Nodes[0], f.State.Nodes[1]
	code := evictFrom(t, f, from, to, u.Cid)
	if code != http.StatusUnauthorized && code != http.StatusForbidden {
		t.Errorf("an unprivileged process on %s asking %s to evict %s over WireGuard with only the marker header: want the handler's 401/403, got %d",
			from.Name, to.Name, u.Cid, code)
	}
	waitContent(t, n.Client.PinTo(to.PublicIP), tenancy.Owner(n), u.Cid, data)
}

// evictFrom sends the eviction request from an unprivileged user on from to
// to's gateway over WireGuard and returns the HTTP status the handler gave.
// A probe that got no HTTP answer (curl failed, "000") fails the test: the
// property is about what the handler decides, so it must be reached.
func evictFrom(t *testing.T, f *fleet.Fleet, from, to fleet.Node, cid string) int {
	t.Helper()
	cmd := fmt.Sprintf(`sudo -n -u nobody curl -sS --max-time 20 -o /dev/null -w '%%{http_code}' -X POST `+
		`-H 'X-Orama-Internal-Auth: %s' -H 'Content-Type: application/json' -d '{"cid":"%s"}' http://%s:%d%s`,
		evictMarker, cid, to.WGIP, gatewayPort, pathEvict)
	out := f.Exec(t, from, cmd)
	var status int
	if _, err := fmt.Sscanf(strings.TrimSpace(out.Stdout), "%d", &status); err != nil || status == 0 || out.Exit != 0 {
		t.Fatalf("%s: the eviction probe to %s got no HTTP answer (exit %d, status %q): %s", from.Name, to.Name, out.Exit, out.Stdout, out.Stderr)
	}
	return status
}
