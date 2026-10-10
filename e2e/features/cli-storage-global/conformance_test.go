//go:build e2e_fleet

package clistorageglobal

import (
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/cliconf"
	"github.com/DeBrosOfficial/network/e2e/harness"
)

// TestConformance_storageGlobalClusterChainCommands runs the generic checks
// (help matches docs/whitepaper/technical-reference/appendices/d-cli-reference.md, --json accepted, unknown flags and
// subcommands are usage errors) on every `orama storage` and `orama global`
// command and the two on-chain cluster commands.
func TestConformance_storageGlobalClusterChainCommands(t *testing.T) {
	t.Parallel()
	cli := harness.CLI(t)
	cliconf.Conformance(t, cli, cli.NoWallet(t), cliconf.LoadReference(t),
		"orama storage", "orama global", "orama cluster register-onchain", "orama cluster retire-onchain")
}
