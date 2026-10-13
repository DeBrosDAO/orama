//go:build e2e_fleet

package externalvantage

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/edge"
	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// probes are the run's vantage points in other locations, or the test does
// not apply (feature.yaml requires.probe; e2e/README.md).
func probes(t *testing.T) (*fleet.Fleet, []fleet.Node) {
	t.Helper()
	f := harness.Fleet(t)
	if f.State.IsStagenet() {
		harness.SkipNotApplicable(t, "the stagenet target has no probe VM: the existing cluster is only tested, never provisioned")
	}
	if len(f.State.Probes) == 0 {
		harness.SkipNotApplicable(t, "the run has no probe VM (state.probes is empty); provision one with requires.probe to test from outside the fleet's network")
	}
	return f, f.State.Probes
}

// caOnProbe puts the run's pinned roots on p (removed at cleanup) and
// returns the path.
func caOnProbe(t *testing.T, f *fleet.Fleet, p fleet.Node) string {
	t.Helper()
	pem, err := os.ReadFile(f.State.CAFile)
	if err != nil {
		t.Fatalf("read the run's CA bundle: %v", err)
	}
	path := "/tmp/" + edge.RandomLabel(t, "e2e-roots-") + ".pem"
	f.WriteFile(t, p, path, pem, 0o644)
	return path
}

// requireTool fails unless the probe has the command: a probe is provisioned
// with the stock image, which ships these.
func requireTool(t *testing.T, f *fleet.Fleet, p fleet.Node, tool string) {
	t.Helper()
	if out := f.Exec(t, p, "command -v "+tool); out.Exit != 0 {
		t.Fatalf("%s has no %s: the probe image must provide it", p.Name, tool)
	}
}

// relayBudget bounds WebRTC provisioning: records, then a listening relay.
const relayBudget = 5 * time.Minute

// relayIPs waits until the registry holds the namespace's TURNS records
// (turn-<ns>.<base>, core/pkg/namespace/dns_manager.go CreateTURNRecords)
// and returns their addresses. Only addresses are read.
func relayIPs(t *testing.T, f *fleet.Fleet, name string) []string {
	t.Helper()
	fqdn := edge.Fqdn(fmt.Sprintf("turn-%s.%s", name, f.State.BaseDomain))
	var ips []string
	eventually.Require(t, edge.PollEvery, relayBudget, "TURN records for "+name, func() (bool, error) {
		q, err := infra.IndexQueryAt(t, f, f.State.Nodes[0], "strong",
			"SELECT value FROM dns_records WHERE fqdn = ? AND record_type = 'A' AND is_active = TRUE", fqdn)
		if err != nil {
			return false, err
		}
		ips = nil
		for _, row := range q.Values {
			if v, ok := row[0].(string); ok {
				ips = append(ips, v)
			}
		}
		if len(ips) == 0 {
			return false, fmt.Errorf("no active A record for %s", fqdn)
		}
		return true, nil
	})
	return ips
}
