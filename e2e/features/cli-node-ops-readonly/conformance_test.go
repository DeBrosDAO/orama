//go:build e2e_fleet

package clinodeopsreadonly

import (
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/cliconf"
	"github.com/DeBrosOfficial/network/e2e/harness"
)

// TestConformance_nodeAndMonitorCommands runs the generic checks (help
// matches docs/whitepaper/technical-reference/appendices/d-cli-reference.md, --json accepted, unknown flags and
// subcommands are usage errors, required positional arguments enforced) on
// every `orama node`, `orama status`, `orama nodes` and `orama status`
// command. Help and flag errors never reach a node, so the destructive node
// commands are safe to probe this way.
func TestConformance_nodeAndMonitorCommands(t *testing.T) {
	t.Parallel()
	cli := harness.CLI(t)
	cliconf.Conformance(t, cli, cli.NoWallet(t), cliconf.LoadReference(t),
		"orama node", "orama status", "orama nodes", "orama status")
}
