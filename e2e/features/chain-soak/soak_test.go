//go:build e2e_fleet

package chainsoak

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
	"github.com/DeBrosOfficial/network/e2e/features/internal/realistic"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/chainsoak"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/runctx"
)

const (
	feature = "chain-soak"
	// EnvSoak is how long the soak runs, as a Go duration (E2E_CHAIN_SOAK=24h). The runner plans the
	// package only when it is set (feature.yaml, opt_in) and adds the duration to the package's budget.
	EnvSoak = "E2E_CHAIN_SOAK"
	// MinSoak and MaxSoak bound the duration: below ten minutes there is no series to judge, and the
	// runner's fleet is not meant to be held for more than a week.
	MinSoak = 10 * time.Minute
	MaxSoak = 7 * 24 * time.Hour

	// sampleEvery is how often the cluster is sampled and a slice of the load is sent.
	sampleEvery = 5 * time.Minute
	// batchEvery is how long one signed batch of transfers lasts. A batch is signed ahead in one go
	// (sequence numbers are consecutive), so an hour of load is signed in a few minutes.
	batchEvery = time.Hour
	// batchTransfers and transfersPerCycle make the load: one public transfer every ten seconds.
	batchTransfers    = 360
	transfersPerCycle = 30
	// deliverSlack is how many of the newest transfers may still be unconfirmed when a cycle checks
	// that the others are in blocks as another validator sees them.
	deliverSlack = 0
	// pollSlack lets one cycle run past its slot without the wait giving up.
	pollSlack = 30 * time.Minute
	// settleBudget is the time left for the final analysis, the timeline file and the cleanups.
	settleBudget = 20 * time.Minute
)

// soak is one run of the chain soak.
type soak struct {
	c     *chain.Chain
	start time.Time
	tl    chainsoak.Timeline
	lim   chainsoak.Limits
	sent  int
}

// soakDuration reads EnvSoak, skipping (not covered) when it is not set, and refusing a value that is
// no duration, is outside [MinSoak, MaxSoak] or does not fit in the budget the runner gave the package.
func soakDuration(t *testing.T) time.Duration {
	t.Helper()
	raw := os.Getenv(EnvSoak)
	if raw == "" {
		harness.SkipNotApplicable(t, EnvSoak+" is not set: the chain soak runs only when asked, e.g. E2E_CHAIN_SOAK=24h in the environment of the runner")
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < MinSoak || d > MaxSoak {
		t.Fatalf("%s=%q must be a Go duration between %s and %s", EnvSoak, raw, MinSoak, MaxSoak)
	}
	if left, ok := realistic.StageRemaining(t); ok && d+settleBudget > left {
		t.Fatalf("%s=%s does not fit: the package has %s left and the soak needs its duration and %s to settle; the runner adds %s to the stage's timeout when the variable is set in its environment",
			EnvSoak, d, left.Round(time.Minute), settleBudget, EnvSoak)
	}
	return d
}

// TestChainSoak_steadyMixedLoadHoldsTheChain drives public transfers and reads at a steady rate for
// E2E_CHAIN_SOAK and samples the chain every five minutes (timeline.json in the artifact dir). It
// fails at once on a divergence of the validators' hashes or a halt, and at the end on memory that
// only grows, a daemon that restarted, an indexer that fell behind or a mempool that did not drain
// (chainsoak.DefaultLimits).
func TestChainSoak_steadyMixedLoadHoldsTheChain(t *testing.T) {
	d := soakDuration(t)
	r := &soak{c: chain.New(t), lim: chainsoak.DefaultLimits()}
	nodes := r.c.Nodes()
	if len(nodes) < 2 {
		t.Fatalf("the run has %d validators: the soak compares hashes between at least two", len(nodes))
	}
	r.c.RequireSameChain(t, nodes, chain.EpochBudget, "before the soak")
	r.start = time.Now()
	r.tl = chainsoak.Timeline{Started: r.start, Duration: d, Interval: sampleEvery}
	for n := 0; time.Since(r.start) < d && !t.Failed(); n++ {
		left := d - time.Since(r.start)
		t.Run(fmt.Sprintf("hour-%03d", n), func(t *testing.T) { r.batch(t, n, min(batchEvery, left)) })
	}
	r.finish(t)
}

// batch signs a batch of transfers and spends length on them: every sampleEvery it sends a slice,
// checks the earlier ones are in blocks as another validator sees them, and takes a sample.
func (r *soak) batch(t *testing.T, n int, length time.Duration) {
	t.Helper()
	nodes := r.c.Nodes()
	load := r.c.NewTransferLoad(t, nodes[0], fmt.Sprintf("soak%03d", n), batchTransfers)
	observer := nodes[len(nodes)-1]
	ctx, release := runctx.With(t.Context())
	defer release()
	begin, sent := time.Now(), 0
	err := eventually.Poll(ctx, sampleEvery, length+pollSlack, "the soak's cycles", func() (bool, error) {
		sent = r.cycle(t, load, observer, sent)
		return time.Since(begin) >= length, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// cycle is one turn of the soak: load, read, sample, judge. It returns how many of the batch's transfers
// have been sent.
func (r *soak) cycle(t *testing.T, load *chain.TransferLoad, observer fleet.Node, sent int) int {
	t.Helper()
	if sent > deliverSlack {
		load.WaitDelivered(t, observer, sent-deliverSlack)
	}
	next := min(sent+transfersPerCycle, load.Total())
	chain.RequireAccepted(t, "the soak's load", load.Broadcast(t, sent, next))
	r.sent += next - sent
	r.tl.Samples = append(r.tl.Samples, r.sample(t, r.sent))
	r.record(t)
	for _, f := range chainsoak.Analyze(r.tl, r.lim) {
		if f.Fatal() {
			t.Fatalf("the chain soak stopped at %s: %s: %s", time.Since(r.start).Round(time.Second), f.Kind, f.Detail)
		}
	}
	return next
}

// record writes the timeline and its summary to the artifact dir.
func (r *soak) record(t testing.TB) {
	t.Helper()
	findings := chainsoak.Analyze(r.tl, r.lim)
	realistic.WriteJSON(t, r.c.F, feature, "timeline.json", map[string]any{
		"summary": chainsoak.Summarize(r.tl, findings), "findings": findings, "limits": r.lim, "timeline": r.tl,
	})
}

// finish judges the whole series: every finding fails the test, and the chain must still agree.
func (r *soak) finish(t *testing.T) {
	t.Helper()
	if len(r.tl.Samples) > 0 {
		r.record(t)
	}
	for _, f := range chainsoak.Analyze(r.tl, r.lim) {
		t.Errorf("%s: %s", f.Kind, f.Detail)
	}
	r.c.RequireSameChain(t, r.c.Nodes(), chain.EpochBudget, "after the soak")
}
