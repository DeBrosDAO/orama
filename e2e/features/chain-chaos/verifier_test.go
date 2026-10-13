//go:build e2e_fleet

package chainchaos

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
	"github.com/DeBrosOfficial/network/e2e/features/internal/realistic"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

const (
	// verifierBinary is the file name of the out-of-process shielded verifier
	// oramad runs as a child (chain/app ShieldedVerifierBinary).
	verifierBinary = "orama-orchard-verifier"
	// oramadBinary is the chain process, whose pid must not change.
	oramadBinary = "oramad"
	// verifierTxs is the public transfers broadcast after the child is killed.
	verifierTxs = 30
	// verifierWorst is the most the verifier test takes.
	verifierWorst = chain.TxBudget + healBudget + chain.EpochBudget
)

// unitProcsScript prints the pids of the chain unit's processes whose
// executable is named exe. The unit may run oramad directly (a run chain) or
// under cosmovisor (an installed node), so the verifier is found by its place in
// the unit's cgroup, not as a child of the main process.
func unitProcsScript(exe string) string {
	return fmt.Sprintf(`cg=$(systemctl show -p ControlGroup --value %s); for p in $(cat "/sys/fs/cgroup$cg/cgroup.procs" 2>/dev/null); do `+
		`[ "$(basename "$(readlink /proc/$p/exe 2>/dev/null)")" = %s ] && echo $p; done; true`, chain.Unit, exe)
}

// unitProcs is unitProcsScript run on n.
func unitProcs(t testing.TB, c *chain.Chain, n fleet.Node, exe string) []string {
	t.Helper()
	return strings.Fields(c.MustRun(t, n, chain.QueryBudget, unitProcsScript(exe)).Stdout)
}

// killVerifierScript signals every verifier process of the chain unit with
// SIGKILL and prints how many it signalled.
func killVerifierScript() string {
	return fmt.Sprintf("n=0; for p in $(%s); do kill -KILL $p && n=$((n+1)); done; echo killed $n", unitProcsScript(verifierBinary))
}

// TestShieldedVerifier_killedChildDoesNotMoveConsensus: the out-of-process
// shielded verifier (chain/x/shielded/verify/orchardproc) is a child of
// oramad. Its process is killed on one validator and public transfers keep
// arriving: oramad must not die or restart, and the validator must stay on the
// same blocks and app hash as its peers, which is the property a verifier
// fault must never break: a node must not commit a result its peers did not.
//
// What this does NOT reach, and why: the child is started when the node starts
// and again by the next verification, and a public transfer never asks it, so
// it is killed once and stays down; a verification in flight needs a bundle
// with a valid proof (the Orchard library, the first verifier, rejects anything
// else before the child is asked), which only the Rust wallet
// (chain/x/shielded/wallet) builds, bound to the chain id and the note tree.
// A fleet run has neither the library nor the child (chain-deploy.sh builds
// oramad with CGO_ENABLED=0 and installs no verifier binary: a devnet id is
// exempt, chain/app/shielded_verifiers.go verifiersOptional), so on it this
// test is not applicable. By the code a child that dies during a verification
// in FinalizeBlock is no verdict and records no result: the keeper stops the node
// with a VerifierHalt and the block is executed again after the restart, so no
// node commits a result its peers did not
// (chain/x/shielded/keeper/admit.go checkInBlock). The agreement check below is
// the assertion that holds that property once shielded traffic can drive the
// verifier during the kills; the fleet cannot produce it today.
func TestShieldedVerifier_killedChildDoesNotMoveConsensus(t *testing.T) {
	c := chain.New(t)
	victim, survivors, found := verifierNode(t, c)
	if !found {
		harness.SkipNotApplicable(t, "no validator runs the out-of-process shielded verifier: a run chain's oramad is built without the Orchard library and ships no "+verifierBinary+" (e2e/scripts/chain-deploy.sh); it applies on a chain whose nodes were installed by `orama setup`")
	}
	realistic.RequireFaultBudget(t, "the verifier kill", verifierWorst)
	ref := survivors[0]
	load := c.NewTransferLoad(t, ref, "verifier", verifierTxs)
	pids, restarts := unitProcs(t, c, victim, oramadBinary), systemdValue(t, c, victim, "NRestarts")
	c.AdvanceAtCleanupWithin(t, healBudget)

	requireKilled(t, c, victim)
	chain.RequireAccepted(t, "after the verifier was killed", load.Broadcast(t, 0, load.Total()))

	if got := unitProcs(t, c, victim, oramadBinary); strings.Join(got, " ") != strings.Join(pids, " ") {
		t.Errorf("%s: oramad runs as %v, was %v: killing the verifier took it down", victim.Name, got, pids)
	}
	if got := systemdValue(t, c, victim, "NRestarts"); got != restarts {
		t.Errorf("%s: systemd restarted %s (%s restarts, was %s)", victim.Name, chain.Unit, got, restarts)
	}
	c.RequireSameChain(t, c.Nodes(), healBudget, "after the verifier was killed")
	load.WaitDelivered(t, ref, load.Total())
	load.RequireBalance(t, victim, load.Total())
}

// verifierNode is the first validator that runs the verifier child, and the
// others as survivors.
func verifierNode(t testing.TB, c *chain.Chain) (victim fleet.Node, survivors []fleet.Node, found bool) {
	t.Helper()
	for i, n := range c.Nodes() {
		if len(unitProcs(t, c, n, verifierBinary)) > 0 {
			rest := append(append([]fleet.Node{}, c.Nodes()[:i]...), c.Nodes()[i+1:]...)
			return n, rest, true
		}
	}
	return fleet.Node{}, nil, false
}

// requireKilled kills the verifier child on n and checks it signalled one.
func requireKilled(t testing.TB, c *chain.Chain, n fleet.Node) {
	t.Helper()
	out := c.MustRun(t, n, chain.QueryBudget, killVerifierScript())
	_, count, _ := strings.Cut(strings.TrimSpace(out.Stdout), "killed ")
	if k, err := strconv.Atoi(count); err != nil || k == 0 {
		t.Fatalf("%s: no verifier child was signalled (%q)", n.Name, out.Stdout)
	}
}
