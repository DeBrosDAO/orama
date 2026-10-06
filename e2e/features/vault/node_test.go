//go:build e2e_fleet

package vault

import (
	"fmt"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness"
)

const (
	vaultUnit = "orama-namespace-vault@index.service"
	// guardianPort is the guardian's client port in the code
	// (core/pkg/constants VaultHTTPPort = 10106); docs/vault/API.md says 7500.
	guardianPort = 10106
	// peerPort is the peer protocol port docs/vault/API.md says nothing
	// listens on yet.
	peerPort = 7501
)

// TestGuardian_unitAndOverlayOnly: every node runs the guardian, which is
// not reachable on a public address (docs/vault/API.md: overlay only).
func TestGuardian_unitAndOverlayOnly(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	for _, n := range f.State.Nodes {
		if s := f.Unit(t, n, vaultUnit); s != "active" {
			t.Errorf("%s: %s is %q", n.Name, vaultUnit, s)
		}
		found := false
		for _, l := range f.Listeners(t, n) {
			if l.Port != guardianPort {
				continue
			}
			found = true
			if l.Public() || l.Addr == n.PublicIP {
				t.Errorf("%s: the guardian listens on %s", n.Name, l.Addr)
			}
		}
		if !found {
			t.Errorf("%s: nothing listens on the guardian port %d", n.Name, guardianPort)
		}
	}
}

// TestGuardian_documentedNotDone: what docs/vault/API.md says is not built
// yet is not: guardian health is "degraded" (no peer discovery), the
// guardian list is empty, and nothing listens on the peer port 7501.
func TestGuardian_documentedNotDone(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	for _, n := range f.State.Nodes {
		base := fmt.Sprintf("http://%s:%d", n.WGIP, guardianPort)
		health := f.MustExec(t, n, "curl -sS --max-time 10 "+base+"/v1/vault/health").Stdout
		if !strings.Contains(health, `"degraded"`) {
			t.Errorf("%s: guardian health %.200s, documented as degraded until peer discovery exists", n.Name, health)
		}
		list := f.MustExec(t, n, "curl -sS --max-time 10 "+base+"/v1/vault/guardians").Stdout
		if !strings.Contains(strings.ReplaceAll(list, " ", ""), `"guardians":[]`) {
			t.Errorf("%s: guardian list %.200s, documented as empty", n.Name, list)
		}
		for _, l := range f.Listeners(t, n) {
			if l.Port == peerPort {
				t.Errorf("%s: something listens on the peer port %d (%s)", n.Name, peerPort, l.Process)
			}
		}
	}
}

// TestGuardian_directPushNeedsSession: a guardian refuses a push without an
// X-Session-Token from the node itself (docs/vault/API.md: 401).
func TestGuardian_directPushNeedsSession(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := f.State.Nodes[0]
	cmd := fmt.Sprintf(`curl -sS --max-time 10 -o /dev/null -w '%%{http_code}' -X POST -H 'Content-Type: application/json' `+
		`-d '{"identity":"%s","share":"AQI=","version":1}' http://%s:%d/v1/vault/push`, strings.Repeat("a", 64), n.WGIP, guardianPort)
	if code := strings.TrimSpace(f.MustExec(t, n, cmd).Stdout); code != "401" {
		t.Errorf("guardian push without a session: %s, want 401", code)
	}
}
