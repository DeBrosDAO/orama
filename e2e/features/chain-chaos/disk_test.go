//go:build e2e_fleet

package chainchaos

import (
	"fmt"
	"path"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
	"github.com/DeBrosOfficial/network/e2e/features/internal/realistic"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

const (
	mib = 1 << 20
	// rootMarginMB is what the fill leaves free for root (the reserved blocks
	// of an ext4 filesystem): enough for sshd, journald and the harness. Every
	// other user of that filesystem sees none of it, which is why the test only
	// runs where that filesystem holds the chain and nothing else.
	rootMarginMB = 64
	// starvedMaxMB is the most a non-root user may still be able to write for
	// the fill to count as a full disk for the chain.
	starvedMaxMB = 2
	// haltBudget bounds the chain noticing it can not write.
	haltBudget = 5 * time.Minute
	// stallPolls is how many polls in a row at one height count as a halt.
	stallPolls = 4
	// outOfSpace is what the operating system's error for ENOSPC says.
	outOfSpace = "no space left on device"
	// rootMount is the mount point of the root filesystem, which also holds
	// rqlite, ipfs, olric and the gateway of the node.
	rootMount = "/"
	// diskWorst is the most the disk test takes with its recovery.
	diskWorst = haltBudget + observeFor + healBudget
)

// fillChainVolume fills the chain home's own filesystem with one preallocated
// file until root has rootMarginMB left, which leaves every non-root user of it
// (the chain alone: the caller has required the chain home to be its own
// filesystem) with none. The file is removed at cleanup and its removal
// checked; the returned function removes it now. It is not applicable on a
// filesystem that keeps too little for root to starve non-root users that way.
func fillChainVolume(t testing.TB, c *chain.Chain, n fleet.Node, fs chain.FS) (release func()) {
	t.Helper()
	file := path.Join(fs.Mount, fmt.Sprintf("e2e-chaindisk-%s-%d", c.F.State.RunID, time.Now().UnixNano()))
	release = func() { c.CleanupExec(t, n, "rm -f "+fleet.ShellQuote(file)+" && ! test -e "+fleet.ShellQuote(file)) }
	t.Cleanup(release)
	fill := fs.Free - rootMarginMB*mib
	if fill <= 0 {
		t.Fatalf("%s: %s has %d MiB free, no more than the %d MiB margin kept for root", n.Name, fs.Mount, fs.Free/mib, rootMarginMB)
	}
	c.MustRun(t, n, chain.QueryBudget, fmt.Sprintf("fallocate -l %d %s", fill, fleet.ShellQuote(file)))
	if after := c.StatFS(t, n); after.Avail > starvedMaxMB*mib {
		release()
		harness.SkipNotApplicable(t, fmt.Sprintf("%s keeps no blocks reserved for root (%d MiB still writable by the chain's user after the fill): the chain cannot be starved without starving the whole node", fs.Mount, after.Avail/mib))
	}
	return release
}

// halted polls until n stops committing: its RPC stops answering, or its
// height stays put for stallPolls polls.
func halted(t *testing.T, c *chain.Chain, n fleet.Node) error {
	t.Helper()
	last, stalls := int64(-1), 0
	return eventually.Poll(t.Context(), chain.PollEvery, haltBudget, n.Name+" to stop committing on a full disk", func() (bool, error) {
		s, err := c.NodeStatus(t, n)
		if err != nil {
			return true, nil
		}
		if s.Height == last {
			stalls++
		} else {
			last, stalls = s.Height, 0
		}
		return stalls >= stallPolls, nil
	})
}

// journalMentions counts the lines of n's chain journal since the unix time
// since that contain needle (case-insensitive).
func journalMentions(t testing.TB, c *chain.Chain, n fleet.Node, since int64, needle string) int {
	t.Helper()
	out := c.Run(t, n, chain.QueryBudget, fmt.Sprintf("journalctl -u %s --since @%d --no-pager -o cat | grep -ci %s; true", chain.Unit, since, fleet.ShellQuote(needle)))
	count, err := strconv.Atoi(strings.TrimSpace(out.Stdout))
	if err != nil {
		t.Fatalf("%s: journal search printed %q: %v", n.Name, out.Stdout, err)
	}
	return count
}

// ensureChainActiveAtCleanup starts the chain unit at cleanup when it is not
// running (reset of a failed state included), before the cleanups registered
// earlier wait for the chain to move.
func ensureChainActiveAtCleanup(t testing.TB, c *chain.Chain, n fleet.Node) {
	t.Helper()
	t.Cleanup(func() {
		c.CleanupExec(t, n, fmt.Sprintf("systemctl is-active --quiet %[1]s || { systemctl reset-failed %[1]s; systemctl start %[1]s; }", chain.Unit))
	})
}

// TestChainDisk_fullVolumeHaltsWithALogLineAndRecovers: the filesystem that
// holds one validator's chain home is filled until no non-root user can write
// (root keeps rootMarginMB). The validator must stop committing and say why in
// its journal ("no space left on device"), not run on or corrupt its stores;
// the others commit blocks if and only if they hold more than 2/3 of the voting
// power; once the space is freed the unit is running again by itself, the
// validator replays to the head, and every node holds the same blocks and app
// hash. It runs only where the chain home is a filesystem of its own: on the
// root filesystem the fill would starve the node's rqlite, ipfs, olric and
// gateway too, which this test never does.
func TestChainDisk_fullVolumeHaltsWithALogLineAndRecovers(t *testing.T) {
	c := chain.New(t)
	victim, survivors := roles(t, c)
	fs := c.StatFS(t, victim)
	if fs.Mount == rootMount {
		harness.SkipNotApplicable(t, fmt.Sprintf("%s keeps the chain home on the root filesystem, shared with rqlite, ipfs, olric and the gateway: filling it would starve them too; it applies where the chain home is a filesystem of its own", victim.Name))
	}
	realistic.RequireFaultBudget(t, "the full disk", diskWorst)
	ref := survivors[0]
	quorum := c.SurvivorsHaveQuorum(t, ref, victim)
	power := c.MustStatus(t, victim).VotingPow
	c.AdvanceAtCleanupWithin(t, healBudget)
	ensureChainActiveAtCleanup(t, c, victim)
	since, err := strconv.ParseInt(strings.TrimSpace(c.Run(t, victim, chain.QueryBudget, "date +%s").Stdout), 10, 64)
	if err != nil {
		t.Fatalf("%s: the clock: %v", victim.Name, err)
	}

	release := fillChainVolume(t, c, victim, fs)
	if err := halted(t, c, victim); err != nil {
		t.Fatalf("%s: %v (the chain ran on with no writable space)", victim.Name, err)
	}
	from := c.MustStatus(t, ref).Height
	requireQuorumRule(t, c, ref, quorum, from, "while "+victim.Name+"'s disk is full")

	release()
	if journalMentions(t, c, victim, since, outOfSpace) == 0 {
		t.Errorf("%s stopped committing but its journal holds no %q line: an operator would not learn the disk is full", victim.Name, outOfSpace)
	}
	c.RequireSameChain(t, c.Nodes(), healBudget, "after the disk was freed")
	requirePowerKept(t, c, victim, power, "a full disk")
	if got := c.F.Unit(t, victim, chain.Unit); got != stateActive {
		t.Errorf("%s: the chain unit is %s after the space was freed", victim.Name, got)
	}
}
