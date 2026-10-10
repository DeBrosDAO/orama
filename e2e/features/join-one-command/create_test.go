//go:build e2e_fleet

package joinonecommand

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/pkg/netregistry"
)

const (
	// releaseRootEnv names the release-root.json the created network's releases
	// are verified against; releaseRepoEnv and releaseChannelEnv, when set, name
	// the repository and channel it is fetched from. Without the root the test is
	// not applicable: a network cannot be created without one.
	releaseRootEnv    = "E2E_SETUP_RELEASE_ROOT"
	releaseRepoEnv    = "E2E_SETUP_RELEASE_REPO"
	releaseChannelEnv = "E2E_SETUP_RELEASE_CHANNEL"
	// createdNetwork and createdChainID are a network of the run's own, on its own
	// three fresh servers. The chain id is a devnet's: it needs no production
	// committee and may carry the faucet. The servers are new every run, so the
	// id is never reused on a machine that ran it.
	createdNetwork = "e2ecreate"
	createdChainID = "orama-e2e-devnet-1"
	createdSeats   = 3
)

// createArgs is `orama setup --create-network` for the fresh servers, unattended.
func createArgs(t *testing.T, publishDir string, extras ...harness.Extra) []string {
	t.Helper()
	root := os.Getenv(releaseRootEnv)
	if root == "" {
		harness.SkipNotApplicable(t, "set "+releaseRootEnv+" to the release-root.json the created network's releases are verified against")
	}
	args := []string{"--create-network", createdNetwork, "--chain-id", createdChainID, "--release-root", root, "--publish-dir", publishDir}
	if repo := os.Getenv(releaseRepoEnv); repo != "" {
		args = append(args, "--release-repo", repo)
	}
	if channel := os.Getenv(releaseChannelEnv); channel != "" {
		args = append(args, "--channel", channel)
	}
	return append(append([]string{"setup", "--yes"}, args...), machineArgs(t, extras...)...)
}

// TestSetup_createANetworkOnThreeFreshServers is the creation of a network from
// nothing: three fresh servers become the bootstrap committee of a chain that did
// not exist, the genesis is built from their keys, the operator and the nodes are
// registered, and the description of the network is written for publishing. The
// second step runs the same command again and finds nothing to do.
func TestSetup_createANetworkOnThreeFreshServers(t *testing.T) {
	f := harness.Fleet(t)
	cli := isolatedCLI(t)
	extras := []harness.Extra{
		harness.ExtraNode(t, "join-one-create-1", extraLocation),
		harness.ExtraNode(t, "join-one-create-2", extraLocation),
		harness.ExtraNode(t, "join-one-create-3", extraLocation),
	}
	publishDir := filepath.Join(t.TempDir(), "networks")
	args := createArgs(t, publishDir, extras...)

	var genesis []byte
	steps := []struct {
		name string
		run  func(t *testing.T)
	}{
		{"threeFreshServersBecomeTheCommitteeOfANewChain", func(t *testing.T) {
			res := infra.RunFor(t, cli, infra.InstallBudget*2, args...)
			infra.ExpectExit(t, res, infra.ExitOK, "Done.", "make -C core sync-networks",
				"orama network add https://orama.network/networks/"+createdNetwork+"/manifest.json")
			for _, e := range extras {
				if out := f.Exec(t, e.Node, "systemctl is-active "+infra.ChainUnit); out.Exit != 0 {
					t.Errorf("%s is not active on %s after the creation:\n%s", infra.ChainUnit, e.PublicIP, out.Stdout)
				}
			}
			doc := requireHealthy(t, cli, createdNetwork, createdSeats)
			for _, n := range doc.Nodes {
				if n.Chain == nil || !n.Chain.Responsive || n.Chain.CatchingUp || !n.Chain.Validator {
					t.Errorf("%s: chain %+v, want an answering validator (the committee seat)", n.Host, n.Chain)
				}
			}
			genesis = publishedGenesis(t, publishDir)
		}},
		{"theSameCommandAgainKeepsTheGenesis", func(t *testing.T) {
			res := infra.RunFor(t, cli, infra.InstallBudget, args...)
			infra.ExpectExit(t, res, infra.ExitOK, "genesis skipped", "the genesis the machines carry is kept")
			if again := publishedGenesis(t, publishDir); string(again) != string(genesis) {
				t.Error("running the creation again changed the genesis it published")
			}
			requireHealthy(t, cli, createdNetwork, createdSeats)
		}},
	}
	for _, s := range steps {
		if !t.Run(s.name, s.run) {
			t.Fatalf("step %s failed; the later steps depend on it", s.name)
		}
	}
}

// publishedGenesis reads networks/<name>/ as setup wrote it and checks the
// manifest pins the genesis beside it, for the chain id asked for.
func publishedGenesis(t *testing.T, publishDir string) []byte {
	t.Helper()
	dir := filepath.Join(publishDir, createdNetwork)
	raw, err := os.ReadFile(filepath.Join(dir, netregistry.ManifestFile))
	if err != nil {
		t.Fatalf("the creation wrote no manifest: %v", err)
	}
	m, err := netregistry.ParseManifest(raw)
	if err != nil {
		t.Fatalf("the manifest written does not parse: %v", err)
	}
	genesis, err := os.ReadFile(filepath.Join(dir, netregistry.GenesisFile))
	if err != nil {
		t.Fatalf("the creation wrote no genesis: %v", err)
	}
	if err := m.VerifyGenesis(genesis); err != nil {
		t.Errorf("the manifest does not pin the genesis written: %v", err)
	}
	if m.ChainID != createdChainID || !m.Faucet || len(m.Seeds) != createdSeats {
		t.Errorf("manifest = %+v, want chain %s with the faucet and %d seeds", m, createdChainID, createdSeats)
	}
	if _, err := os.Stat(filepath.Join(dir, netregistry.ReleaseRootFile)); err != nil {
		t.Errorf("the creation wrote no release root: %v", err)
	}
	return genesis
}

// TestSetup_aProductionChainIdNeedsAProductionCommittee: a chain id without
// -stagenet-, -devnet- or -localnet- is a production one and needs 30 bootstrap
// validators; the refusal is a usage error before any server is contacted, and it
// names a test network's chain id as the way out.
func TestSetup_aProductionChainIdNeedsAProductionCommittee(t *testing.T) {
	cli := isolatedCLI(t)
	res := infra.Run(t, cli, "setup", "--yes", "--create-network", "prod", "--chain-id", "orama-1", "--release-root", "release-root.json",
		"--ip", unusedIP, "--host-key", "SHA256:abc")
	infra.ExpectExit(t, res, infra.ExitUsage, "at least 30", "-stagenet-")
}

// TestSetup_creatingNeedsTheChainIdAndTheReleaseRoot: --create-network without its
// chain id or its release root is refused, and the creation flags mean nothing
// without --create-network.
func TestSetup_creatingNeedsTheChainIdAndTheReleaseRoot(t *testing.T) {
	cli := isolatedCLI(t)
	common := []string{"setup", "--yes", "--ip", unusedIP, "--host-key", "SHA256:abc"}
	noChain := append([]string{"--create-network", "x", "--release-root", "release-root.json"}, common...)
	infra.ExpectExit(t, infra.Run(t, cli, noChain...), infra.ExitUsage, "--chain-id")
	noRoot := append([]string{"--create-network", "x", "--chain-id", "orama-x-devnet-1"}, common...)
	infra.ExpectExit(t, infra.Run(t, cli, noRoot...), infra.ExitUsage, "--release-root")
	stray := append([]string{"--chain-id", "orama-x-devnet-1", "--name", "alice"}, common...)
	infra.ExpectExit(t, infra.Run(t, cli, stray...), infra.ExitUsage, "--create-network")
	both := append([]string{"--create-network", "x", "--network", "stagenet", "--chain-id", "orama-x-devnet-1", "--release-root", "r.json"}, common...)
	infra.ExpectExit(t, infra.Run(t, cli, both...), infra.ExitUsage, "--network")
}
