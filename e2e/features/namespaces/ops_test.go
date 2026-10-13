//go:build e2e_fleet

package namespaces

import (
	"context"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

// TestNamespaceRepair_onANodeIsIdempotent: `orama namespace repair` runs on a
// node and talks to that node's gateway over the WireGuard address
// (docs/whitepaper/technical-reference/appendices/d-cli-reference.md "orama namespace repair"). On a healthy namespace it
// succeeds and changes nothing; run twice it succeeds twice; an unknown
// namespace fails; run off a node it fails with a message saying so.
func TestNamespaceRepair_onANodeIsIdempotent(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{})
	members := tenancy.Members(t, f, n.Name)
	node := members[0]
	for range 2 {
		out := f.MustExec(t, node, "orama namespace repair "+n.Name)
		if !strings.Contains(out.Stdout, "repaired") {
			t.Fatalf("repair printed %q", out.Stdout)
		}
	}
	for _, other := range members {
		for _, unit := range tenancy.TenantUnits(n.Name) {
			if s := f.Unit(t, other, unit); s != "active" {
				t.Errorf("%s: %s is %s after repair", other.Name, unit, s)
			}
		}
	}
	if out := f.Exec(t, node, "orama namespace repair e2e-never-created-x"); out.Exit == 0 {
		t.Errorf("repair of an unknown namespace succeeded: %s", out.Stdout)
	}
	res, err := harness.CLI(t).Run(t.Context(), "namespace", "repair", n.Name)
	if err != nil {
		t.Fatal(err)
	}
	if res.Exit == 0 || !strings.Contains(res.Stderr+res.Stdout, "node") {
		t.Errorf("repair off a node: want a failure naming the node requirement, got exit %d %q", res.Exit, res.Stderr)
	}
	tenancy.Get(t, n.Client, "/health", tenancy.Cred{}).Expect(t, 200)
}

// TestNamespaceWebRTC_enableStatusDisable: `orama namespace enable webrtc`
// provisions the feature, webrtc-status reports it, disable removes it, and
// an unknown feature is refused (docs/whitepaper/technical-reference/appendices/d-cli-reference.md "orama namespace
// enable"). The WebRTC data path itself is the realtime features'.
func TestNamespaceWebRTC_enableStatusDisable(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{Via: ns.ViaOperator})
	cli := n.CLI
	if out := cli.MustOK(t, "namespace", "webrtc-status", "--namespace", n.Name).Stdout; !strings.Contains(out, "not enabled") {
		t.Fatalf("a new namespace reports WebRTC: %q", out)
	}
	cli.MustOK(t, "namespace", "enable", "webrtc", "--namespace", n.Name)
	disabled := false
	t.Cleanup(func() {
		if !disabled {
			ctx, cancel := context.WithTimeout(context.Background(), cleanupBudget)
			defer cancel()
			if res, err := cli.Run(ctx, "namespace", "disable", "webrtc", "--namespace", n.Name); err != nil || res.Exit != 0 {
				t.Errorf("cleanup: disabling WebRTC on %s failed: %v %s", n.Name, err, res.Stderr)
			}
		}
	})
	if out := cli.MustOK(t, "namespace", "webrtc-status", "--namespace", n.Name).Stdout; !strings.Contains(out, "Enabled:          yes") {
		t.Fatalf("webrtc-status after enable printed %q", out)
	}
	cli.MustOK(t, "namespace", "disable", "webrtc", "--namespace", n.Name)
	disabled = true
	if out := cli.MustOK(t, "namespace", "webrtc-status", "--namespace", n.Name).Stdout; !strings.Contains(out, "not enabled") {
		t.Fatalf("webrtc-status after disable printed %q", out)
	}
	for _, args := range [][]string{{"enable", "bogus"}, {"disable", "bogus"}, {"enable"}} {
		res, err := cli.Run(t.Context(), append([]string{"namespace"}, args...)...)
		if err != nil {
			t.Fatal(err)
		}
		if res.Exit == 0 {
			t.Errorf("orama namespace %s succeeded", strings.Join(args, " "))
		}
	}
}
