//go:build e2e_fleet

package chaincli

import (
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/cliconf"
	"github.com/DeBrosOfficial/network/e2e/harness"
)

// TestConformance_chainCommands runs the generic checks (help matches
// docs/whitepaper/technical-reference/appendices/d-cli-reference.md, --json accepted, unknown flags and subcommands are
// usage errors, required positional arguments enforced) on `orama chain` and
// every subcommand. `orama chain` with no subcommand prints its help and
// lists them all.
func TestConformance_chainCommands(t *testing.T) {
	t.Parallel()
	cli := harness.CLI(t)
	cliconf.Conformance(t, cli, cli.NoWallet(t), cliconf.LoadReference(t), "orama chain")
}
