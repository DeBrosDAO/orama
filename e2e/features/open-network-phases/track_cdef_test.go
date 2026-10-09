//go:build e2e_fleet

package opennetworkphases

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/monitor"
	"github.com/DeBrosOfficial/network/pkg/constants"
)

const (
	trackC = "plans/open-network/track-c-chain.md"
	trackD = "plans/open-network/track-d-global-storage.md"
	trackE = "plans/open-network/track-e-tor-network.md"
	trackF = "plans/open-network/track-f-rootwallet.md"
	// chainProgress bounds the chain producing a new block.
	chainProgress = 2 * time.Minute
	stageName     = "e2e-b6"
	seedBytes     = 32
	sealReplicas  = "3"
)

// TestPhaseB5_monitorReportsTheChain: the operator's monitor report carries
// a responsive chain section for every node running the chain (B5;
// docs/MONITORING.md "chain").
func TestPhaseB5_monitorReportsTheChain(t *testing.T) {
	phase(t, "B5", "docs/MONITORING.md", "Only on a node with `orama-global-chain.service`", trackB+" B5")
	f := harness.Fleet(t)
	nodes := chainNodes(t, f)
	r := monitor.Fetch(t, harness.CLI(t), f.State.Env)
	for _, n := range nodes {
		rep, err := infra.ReportFor(r, n)
		if err != nil {
			t.Fatal(err)
		}
		var c *monitor.Chain
		if rep.Report != nil {
			c = rep.Report.Chain
		}
		if c == nil || !c.ServiceActive || !c.Responsive || c.ChainID != f.State.ChainID || c.LatestHeight <= 0 {
			t.Errorf("%s: chain section %+v, want the active, responsive %s", n.Name, c, f.State.ChainID)
		}
	}
}

// TestPhaseB6_unverifiedOramadIsNotStaged: stage-oramad refuses a binary
// that does not verify against the adopted release root and stages nothing
// in the cosmovisor layout (B6; docs/CLI_REFERENCE.md "orama global
// stage-oramad").
func TestPhaseB6_unverifiedOramadIsNotStaged(t *testing.T) {
	phase(t, "B6", "docs/CLI_REFERENCE.md", "### orama global stage-oramad", trackB+" B6")
	f := harness.Fleet(t)
	n := chainNodes(t, f)[0]
	metaDir := "/var/tmp/e2e-b6-meta-" + f.State.RunID
	staged := chainHome + "/cosmovisor/upgrades/" + stageName
	t.Cleanup(func() { cleanupPath(t, f, n, metaDir) })
	t.Cleanup(func() { cleanupPath(t, f, n, staged) })
	f.MustExec(t, n, "mkdir -m 0700 "+metaDir)
	res := onNode(t, f, n, "global", "stage-oramad", "--binary", "/usr/lib/orama-global/bin/oramad",
		"--release-metadata", metaDir, "--release-target", "oramad-linux-amd64", "--upgrade", stageName)
	expectVerifyRefusal(t, f, n, res)
	if strings.Contains(res.Stdout, "staged ") {
		t.Errorf("%s: stage-oramad reported staging a binary with no release metadata:\n%s", n.Name, res.Stdout)
	}
	if f.Exec(t, n, "test -e "+staged).Exit == 0 {
		t.Errorf("%s: a refused stage-oramad left %s behind", n.Name, staged)
	}
}

// TestPhaseB7_globalLifecycleCommands: the global role's install and
// lifecycle commands exist and report the running chain in order (B7). Not
// applicable until the checkout documents `orama global status`.
func TestPhaseB7_globalLifecycleCommands(t *testing.T) {
	phase(t, "B7", "docs/CLI_REFERENCE.md", "### orama global status", trackB+" B7")
	f := harness.Fleet(t)
	n := chainNodes(t, f)[0]
	res := onNode(t, f, n, "global", "status")
	if res.Exit != exitOK || !strings.Contains(res.Stdout, "chain") {
		t.Errorf("%s: orama global status exited %d:\n%s%s", n.Name, res.Exit, res.Stdout, f.Redact(res.Stderr))
	}
}

// TestPhaseC_chainProducesBlocks: the L1 of track C runs on the fleet and
// makes blocks. Its modules are asserted by the chain packages (chain-core
// and the stage 8 packages); this is the phase's acceptance that the chain
// the modules live in is alive (docs/CHAIN.md "What's running").
func TestPhaseC_chainProducesBlocks(t *testing.T) {
	phase(t, "C", "docs/CHAIN.md", "### Modules wired", trackC)
	f := harness.Fleet(t)
	n := chainNodes(t, f)[0]
	first := chainHeight(t, f, n)
	eventually.Require(t, pollEvery, chainProgress, "a new block on "+n.Name, func() (bool, error) {
		h := chainHeight(t, f, n)
		if h > first {
			return true, nil
		}
		return false, fmt.Errorf("height %d, was %d", h, first)
	})
}

func chainHeight(t *testing.T, f *fleet.Fleet, n fleet.Node) int64 {
	t.Helper()
	r := monitor.Fetch(t, harness.CLI(t), f.State.Env)
	rep, err := infra.ReportFor(r, n)
	if err != nil || rep.Report == nil || rep.Report.Chain == nil {
		t.Fatalf("%s has no chain report: %v", n.Name, err)
	}
	return rep.Report.Chain.LatestHeight
}

// TestPhaseD_sealedSlotsOpenOnlyWithTheSeeds: a private file sealed for a
// storage deal gives one different ciphertext per slot, each opens back to
// the file with the right seeds, and a wrong seed opens nothing and writes
// nothing (D3; docs/CHAIN.md "Client side").
func TestPhaseD_sealedSlotsOpenOnlyWithTheSeeds(t *testing.T) {
	phase(t, "D", "docs/CLI_REFERENCE.md", "### orama storage seal", trackD+" D3")
	cli := harness.CLI(t).NoWallet(t)
	dir := t.TempDir()
	plain := randomFile(t, dir, "plain.bin", 20<<10)
	seed, repair, wrong := seedFile(t, dir, "seed"), seedFile(t, dir, "repair"), seedFile(t, dir, "wrong")
	nonce := hex.EncodeToString(randomBytes(t, seedBytes))
	outDir := filepath.Join(dir, "slots")
	cli.MustOK(t, "storage", "seal", "--in", plain, "--nonce", nonce, "--out-dir", outDir, "--storage-key-file", seed, "--repair-seed-file", repair, "--replicas", sealReplicas)
	s0, s1 := readSlot(t, outDir, 0), readSlot(t, outDir, 1)
	if bytes.Equal(s0, s1) {
		t.Error("two slots carry the same ciphertext")
	}
	opened := filepath.Join(dir, "opened.bin")
	cli.MustOK(t, "storage", "open", "--in", filepath.Join(outDir, "slot-1"), "--nonce", nonce, "--slot", "1", "--out", opened, "--storage-key-file", seed, "--repair-seed-file", repair)
	if !bytes.Equal(readLocal(t, opened), readLocal(t, plain)) {
		t.Error("slot 1 did not open to the sealed file")
	}
	bad := filepath.Join(dir, "bad.bin")
	res := run(t, cli, "storage", "open", "--in", filepath.Join(outDir, "slot-1"), "--nonce", nonce, "--slot", "1", "--out", bad, "--storage-key-file", wrong, "--repair-seed-file", repair)
	if _, err := os.Stat(bad); res.Exit == exitOK || err == nil {
		t.Errorf("a wrong owner seed opened the slot (exit %d, output written: %t)", res.Exit, err == nil)
	}
}

// TestPhaseE_torListensOnlyWhereARoleIsInstalled: track E (the Orama Tor
// network, docs/TOR_NETWORK.md) is delivered as opt-in roles. A node publishes
// a Tor listener only for a role it was installed with: the ORPort for a
// relay or an authority, the DirPort for an authority, and the node's own Tor
// client stays on loopback. The roles themselves are tor-network.
func TestPhaseE_torListensOnlyWhereARoleIsInstalled(t *testing.T) {
	phase(t, "E", "docs/TOR_NETWORK.md", "Orama Tor network", trackE)
	f := harness.Fleet(t)
	for _, n := range f.State.Nodes {
		dirauth := f.Unit(t, n, constants.GlobalTorDirauthUnit) == "active"
		relay := f.Unit(t, n, constants.GlobalTorRelayUnit) == "active"
		for _, l := range f.Listeners(t, n) {
			if l.Process != "tor" || !l.Public() {
				continue
			}
			switch {
			case l.Port == constants.GlobalTorORPort && (dirauth || relay):
			case l.Port == constants.GlobalTorDirPort && dirauth:
			default:
				t.Errorf("%s: tor listens publicly on %s:%d with no role installed for it", n.Name, l.Addr, l.Port)
			}
		}
	}
}

// TestPhaseF_nodesTrustTheOperatorWallet: every node trusts archives signed
// by the operator's RootWallet (purpose orama-archive through the agent) and
// nothing else: /etc/orama/archive-signers names exactly the run's operator
// (F1/F8 as far as they shipped; docs/SECURITY.md "Signing").
func TestPhaseF_nodesTrustTheOperatorWallet(t *testing.T) {
	phase(t, "F", "docs/SECURITY.md", "wallet:sign:orama-archive", trackF)
	f := harness.Fleet(t)
	want := strings.ToLower(f.State.OperatorAddress)
	for _, n := range f.State.Nodes {
		got := strings.Fields(string(f.ReadFile(t, n, infra.ArchiveSigners)))
		if len(got) != 1 || got[0] != want {
			t.Errorf("%s trusts %v, want only the operator %s", n.Name, got, want)
		}
	}
}

func randomBytes(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}

func randomFile(t *testing.T, dir, name string, size int) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, randomBytes(t, size), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// seedFile writes a 32-byte hex seed, 0600 as the commands require.
func seedFile(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(hex.EncodeToString(randomBytes(t, seedBytes))), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func readSlot(t *testing.T, dir string, i int) []byte {
	t.Helper()
	return readLocal(t, filepath.Join(dir, fmt.Sprintf("slot-%d", i)))
}
