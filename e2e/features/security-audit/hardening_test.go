//go:build e2e_fleet

package securityaudit

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/edge"
	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/features/internal/services"
	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

// exposure reads systemd-analyze's "Overall exposure level for X: 4.2 OK".
var exposure = regexp.MustCompile(`Overall exposure level for \S+: ([0-9.]+) (\S+)`)

// maxExposure: systemd rates 9.0 and above UNSAFE. Every Orama daemon runs
// under the hardening block (docs/whitepaper/technical-reference/vol1/05-privilege-and-filesystem-trust.md "systemd Hardening").
const maxExposure = 9.0

// hardenedUnits are the long-running daemons of every core node.
var hardenedUnits = []string{"orama-node.service", edge.IndexGatewayUnit, edge.IndexRQLiteUnit,
	"orama-namespace-olric@index.service", edge.CaddyUnit, edge.TorUnit}

// TestHardening_systemdExposureRecorded: every daemon's systemd-analyze
// exposure is recorded (the evidence carries the full breakdown) and none
// is rated UNSAFE.
func TestHardening_systemdExposureRecorded(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	for _, n := range f.State.Nodes {
		units := append(append([]string{}, hardenedUnits...), coreDNSIfNameserver(n.Role)...)
		for _, unit := range units {
			out := f.MustExec(t, n, "systemd-analyze security --no-pager "+unit).Stdout
			m := exposure.FindStringSubmatch(out)
			if m == nil {
				t.Errorf("%s: no exposure rating for %s", n.Name, unit)
				continue
			}
			score, err := strconv.ParseFloat(m[1], 64)
			if err != nil || score >= maxExposure || m[2] == "UNSAFE" {
				t.Errorf("%s: %s exposure %s %s, want below %.1f", n.Name, unit, m[1], m[2], maxExposure)
			}
		}
	}
}

func coreDNSIfNameserver(role string) []string {
	if role == "nameserver" {
		return []string{edge.CoreDNSUnit}
	}
	return nil
}

// TestHardening_socketBindEnforcementMatchesTheAlert: whether systemd can
// enforce SocketBindAllow/Deny (+BPF_FRAMEWORK) is what the node report
// says, and a node without it raises the security warning (docs/whitepaper/technical-reference/vol1/29-build-signing-and-release.md
// "Precondition"; website/src/docs/operator/monitoring.mdx "warning (security)").
func TestHardening_socketBindEnforcementMatchesTheAlert(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	reported := services.Subsystem[struct {
		Enforced *bool `json:"socket_bind_enforced"`
	}](t, "system")
	alerts := strings.Join(services.Alerts(t, "security"), "\n")
	for _, n := range f.State.Nodes {
		has := strings.Contains(f.MustExec(t, n, "systemctl --version").Stdout, "+BPF_FRAMEWORK")
		r, ok := reported[n.PublicIP]
		if !ok || r.Enforced == nil || *r.Enforced != has {
			t.Errorf("%s: systemd +BPF_FRAMEWORK %v, report says %+v", n.Name, has, r.Enforced)
		}
		if warned := strings.Contains(alerts, n.PublicIP); warned == has {
			t.Errorf("%s: +BPF_FRAMEWORK %v but security warning present %v", n.Name, has, warned)
		}
	}
}

// TestSecrets_turnSecretSealedAtRest: a namespace's TURN shared secret is
// stored as an enc: envelope, never plaintext (docs/whitepaper/technical-reference/vol1/11-app-deployments.md "Stored
// secrets"). Only a count is read back from the registry, never the value.
func TestSecrets_turnSecretSealedAtRest(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{Via: ns.ViaOperator})
	enableWebRTC(t, n)
	q := infra.IndexQuery(t, f, f.State.Nodes[0],
		"SELECT COUNT(*), SUM(turn_shared_secret LIKE 'enc:%') FROM namespace_webrtc_config WHERE namespace_name = ?", n.Name)
	total, _ := q.Values[0][0].(float64)
	sealed, _ := q.Values[0][1].(float64)
	if total != 1 || sealed != 1 {
		t.Fatalf("%s: %v WebRTC config rows, %v sealed; want one, sealed", n.Name, total, sealed)
	}
}

// enableWebRTC turns WebRTC on for n (created ViaOperator) and off at cleanup.
func enableWebRTC(t *testing.T, n *ns.Namespace) {
	t.Helper()
	n.CLI.MustOK(t, "namespace", "enable", "webrtc", "--namespace", n.Name)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		if res, err := n.CLI.Run(ctx, "namespace", "disable", "webrtc", "--namespace", n.Name); err != nil || res.Exit != 0 {
			t.Errorf("cleanup: disabling WebRTC on %s failed: %v %s", n.Name, err, res.Stderr)
		}
	})
}
