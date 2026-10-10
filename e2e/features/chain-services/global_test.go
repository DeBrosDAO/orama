//go:build e2e_fleet

package chainservices

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
)

// Global services beside oramad (chain/cmd/orama-global, docs/whitepaper/technical-reference/vol2/37-global-nodes.md
// "Global services: orama-global"; units core/pkg/constants/global.go).
const (
	globalBin      = "/usr/lib/orama-global/bin/orama-global"
	providerUnit   = "orama-global-provider.service"
	globalCommands = "provider repair archiver history"
)

// TestGlobalServices_binaryAndUnitsOnEveryNode: when the fleet runs the
// global services, every node has the orama-global binary with the
// provider, repair, archiver and history commands, and the provider unit is
// active. The run's chain deploy (e2e/scripts/chain-deploy.sh) installs only
// oramad today, so on such a fleet the provider, archiver and repair flows
// (docs/whitepaper/technical-reference/vol2/39-chain-architecture.md) do not apply and the test says so.
func TestGlobalServices_binaryAndUnitsOnEveryNode(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	for _, n := range c.Nodes() {
		if c.F.Exec(t, n, "test -x "+globalBin).Exit != 0 {
			harness.SkipNotApplicable(t, n.Name+" has no "+globalBin+": e2e/scripts/chain-deploy.sh installs only oramad; "+
				"deploy orama-global (provider, repair, archiver) on the fleet to exercise docs/whitepaper/technical-reference/vol2/37-global-nodes.md \"Global services\"")
		}
		help := c.F.MustExec(t, n, globalBin+" --help").Stdout
		for _, cmd := range strings.Fields(globalCommands) {
			if !strings.Contains(help, cmd) {
				t.Errorf("%s: orama-global --help does not list %q", n.Name, cmd)
			}
		}
		if s := c.F.Unit(t, n, providerUnit); s != "active" {
			t.Errorf("%s: %s is %s", n.Name, providerUnit, s)
		}
	}
}

// providerMonitorFile is the status file the provider writes every step
// (core/pkg/constants/global.go GlobalMonitorFile in GlobalProviderHome).
const providerMonitorFile = "/var/lib/orama-global/provider/monitor.json"

// TestGlobalServices_monitorShowsTheProvidersDealSlots: a node that runs the
// provider writes held_slots and pending_slots into its monitor.json, and
// `orama monitor node` prints them on the node's Global line
// (website/src/docs/operator/monitoring.mdx, the node report's global section).
func TestGlobalServices_monitorShowsTheProvidersDealSlots(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	for _, n := range c.Nodes() {
		if c.F.Exec(t, n, "test -x "+globalBin).Exit != 0 {
			harness.SkipNotApplicable(t, n.Name+" has no "+globalBin+": e2e/scripts/chain-deploy.sh installs only oramad; "+
				"deploy orama-global (provider) on the fleet to exercise website/src/docs/operator/monitoring.mdx \"global\"")
		}
		body := c.F.MustExec(t, n, "cat "+providerMonitorFile).Stdout
		for _, field := range []string{`"held_slots"`, `"pending_slots"`} {
			if !strings.Contains(body, field) {
				t.Errorf("%s: %s lacks %s: %s", n.Name, providerMonitorFile, field, body)
			}
		}
	}
	res := infra.Run(t, harness.CLI(t), "status", "node", "--env", c.F.State.Env)
	infra.ExpectExit(t, res, infra.ExitOK, "Global:", "slots held")
}
