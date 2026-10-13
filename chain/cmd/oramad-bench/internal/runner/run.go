package runner

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/DeBrosOfficial/network/chain/client/tx"
	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/account"
	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/broadcast"
	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/gen"
	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/report"
	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/shielded"
	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/watch"
)

// Runner is one run in progress.
type Runner struct {
	cfg      Config
	cluster  *Cluster
	views    []nodeView
	senders  []broadcast.Sender
	accounts []tx.Account
	funder   tx.Account
	signer   *signer
	follower *watch.Follower
	sampler  *watch.Sampler
	fees     *watch.FeeSeries
	feeRec   *feeRecorder
	env      *gen.Env

	cache       *shielded.Cache
	cacheReason string
	blockMaxGas int64
	veHeight    int64
	startHeight int64

	mu       sync.Mutex
	warnings []string
}

// Run executes the profile against the nodes of cfg and returns the report. It returns an error for
// a run that could not be carried out; a validator disagreement is not an error but a report whose
// AppHashAgreed is false, which the caller must treat as a failure.
func Run(ctx context.Context, cfg Config) (report.Report, error) {
	cfg = cfg.WithDefaults()
	if err := cfg.Validate(); err != nil {
		return report.Report{}, err
	}
	r, err := newRunner(ctx, cfg)
	if err != nil {
		return report.Report{}, err
	}
	return r.run(ctx)
}

func newRunner(ctx context.Context, cfg Config) (*Runner, error) {
	// Accounts first: deriving one applies the chain's address prefixes to the SDK config, which the
	// client's codec reads when it is built.
	accounts, err := account.DeriveN(cfg.Seed, cfg.Profile.Accounts)
	if err != nil {
		return nil, err
	}
	funder, err := LoadFunder(cfg.FunderKeyFile)
	if err != nil {
		return nil, err
	}
	cluster, err := Dial(ctx, cfg.Nodes, cfg.FeeMultiple, cfg.GasAdjust)
	if err != nil {
		return nil, err
	}
	r := &Runner{cfg: cfg, cluster: cluster, accounts: accounts, funder: funder, fees: &watch.FeeSeries{}}
	r.feeRec = newFeeRecorder(cluster.Clients[0], r.fees)
	readers := make([]watch.MempoolReader, len(cluster.Clients))
	for i, c := range cluster.Clients {
		r.views = append(r.views, nodeView{c})
		r.senders = append(r.senders, broadcast.RPCSender{Client: c.RPC(), Mode: cfg.Broadcast})
		readers[i] = r.views[i]
	}
	r.signer = newSigner(cluster.Chain, accounts)
	r.follower = watch.NewFollower(r.views[0], 0, time.Now)
	r.sampler = watch.NewSampler(readers, 0, time.Now)
	r.env = &gen.Env{
		Chain: cluster.Chain, Accounts: accounts, Seed: cfg.Seed, RunID: runID(), Logf: cfg.logf, Parallel: cfg.Parallel,
	}
	return r, nil
}

func runID() string { return fmt.Sprintf("%x", time.Now().UnixNano()/int64(time.Microsecond)) }

func (r *Runner) warn(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	r.cfg.logf("warning: %s", msg)
	r.mu.Lock()
	r.warnings = append(r.warnings, msg)
	r.mu.Unlock()
}

func (r *Runner) run(ctx context.Context) (report.Report, error) {
	started := time.Now()
	bg, stop := context.WithCancel(ctx)
	defer stop()
	if err := r.startWatching(bg); err != nil {
		return report.Report{}, err
	}
	if err := r.readChain(ctx); err != nil {
		return report.Report{}, err
	}
	r.cfg.logf("funding the bench accounts")
	if err := fundAccounts(ctx, r.cluster.Chain, r.funder, r.accounts, r.cfg.logf); err != nil {
		return report.Report{}, err
	}
	r.loadCache(ctx)
	// A bundle that was sent may have been included, so its nullifier is spent whether or not the
	// run finishes: the cache is written on every way out, and after every type.
	defer r.saveCache()
	var types []report.Type
	for _, tp := range r.cfg.Profile.Types {
		t, err := r.runType(ctx, tp)
		if err != nil {
			return report.Report{}, err
		}
		types = append(types, t)
		r.saveCache()
	}
	return r.finish(ctx, started, types)
}

// startWatching begins following blocks and sampling mempools.
func (r *Runner) startWatching(ctx context.Context) error {
	start, err := r.follower.Start(ctx)
	if err != nil {
		return err
	}
	r.startHeight = start
	r.follower.OnBlock(r.feeRec.OnBlock)
	go r.feeRec.Run(ctx)
	go func() {
		if err := r.follower.Run(ctx); err != nil {
			r.cfg.logf("the block follower stopped: %v", err)
		}
	}()
	go r.sampler.Run(ctx)
	return nil
}

// readChain records the consensus settings the report states.
func (r *Runner) readChain(ctx context.Context) error {
	res, err := r.cluster.Clients[0].RPC().ConsensusParams(ctx, nil)
	if err != nil {
		return fmt.Errorf("read the consensus parameters: %w", err)
	}
	r.blockMaxGas = res.ConsensusParams.Block.MaxGas
	if res.ConsensusParams.ABCI.VoteExtensionsEnableHeight > 0 {
		r.veHeight = res.ConsensusParams.ABCI.VoteExtensionsEnableHeight
	}
	return nil
}

// finish waits for every node to hold the run's last block, compares their headers, and assembles
// the report.
func (r *Runner) finish(ctx context.Context, started time.Time, types []report.Type) (report.Report, error) {
	r.settle(ctx)
	if err := r.followerFailed(); err != nil {
		return report.Report{}, err
	}
	end, err := r.waitAllAt(ctx)
	if err != nil {
		return report.Report{}, err
	}
	if missed := r.feeRec.Missed(); missed > 0 {
		r.warn("the base fee of %d blocks could not be read; the base fee series has gaps", missed)
	}
	r.cfg.logf("comparing app hashes of %d validators at heights %d to %d", len(r.cluster.Addrs), r.startHeight+1, end)
	cmp, err := compareHeaders(ctx, r.cluster, r.startHeight+1, end)
	if err != nil {
		return report.Report{}, err
	}
	return r.assemble(started, types, cmp, end)
}

// followerFailed is the error that stopped the block follower, if one did. Without the follower no
// transaction is ever seen included, so a run that lost it has measured nothing.
func (r *Runner) followerFailed() error {
	if err := r.follower.Err(); err != nil {
		return fmt.Errorf("the block follower stopped, so the run measured nothing after that: %w", err)
	}
	return nil
}

// waitAllAt waits until every node has committed the block after the follower's last, and returns
// that height. The header of a block carries the app hash of the state before it, so comparing
// headers through the block after the run's last also compares the state the last block produced.
func (r *Runner) waitAllAt(ctx context.Context) (int64, error) {
	latest, err := r.views[0].LatestHeight(ctx)
	if err != nil {
		return 0, err
	}
	end := latest + 1
	deadline := time.Now().Add(catchUpWait)
	for i, v := range r.views {
		for {
			h, err := v.LatestHeight(ctx)
			if err != nil {
				return 0, fmt.Errorf("read the height of %s: %w", r.cluster.Addrs[i], err)
			}
			if h >= end {
				break
			}
			if time.Now().After(deadline) {
				return 0, fmt.Errorf("node %s is at height %d, %d behind the first node, %s after the run", r.cluster.Addrs[i], h, end-h, catchUpWait)
			}
			select {
			case <-ctx.Done():
				return 0, ctx.Err()
			case <-time.After(settlePoll):
			}
		}
	}
	return end, nil
}

const catchUpWait = time.Minute
