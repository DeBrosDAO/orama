package runner

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/gen"
	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/profile"
	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/report"
	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/shielded"
)

// runType prepares one transaction type, calibrates its gas, and runs its steps.
func (r *Runner) runType(ctx context.Context, tp profile.TypePlan) (report.Type, error) {
	name, total := tp.Type, tp.TotalTxs()
	g, err := gen.New(name)
	if err != nil {
		return report.Type{}, err
	}
	out := report.Type{Name: name, Description: g.Describe()}
	r.cfg.logf("%s: preparing for %d transactions", name, total)
	if err := g.Prepare(ctx, r.env, total); err != nil {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		return r.skipped(out, err), nil
	}
	gas, err := r.calibrate(ctx, g)
	if err != nil {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		return r.skipped(out, err), nil
	}
	out.GasLimit = gas
	if price, err := r.cluster.Chain.GasPrice(ctx); err == nil {
		out.GasPrice = price.String()
	}
	// What a type handed out may have been included whether or not the run finishes, and a spent
	// nullifier cannot be sent again: record the use on every way out.
	defer r.recordUse(g)
	next := 0
	for i, spec := range tp.Steps {
		r.cfg.logf("%s: step %d of %d (%s)", name, i+1, len(tp.Steps), describeStep(spec))
		step, used, err := r.runStep(ctx, g, spec, next, gas)
		if errors.Is(err, errExhausted) {
			r.warn("%s: the type ran out of transactions before step %d; the later steps did not run", name, i+1)
			break
		}
		if err != nil {
			if ctx.Err() != nil {
				return out, ctx.Err()
			}
			return out, fmt.Errorf("%s: step %d failed: %w", name, i+1, err)
		}
		if err := r.followerFailed(); err != nil {
			return out, err
		}
		next += used
		out.Steps = append(out.Steps, step)
		r.settle(ctx)
	}
	out.Saturation = report.Analyze(out.Steps)
	return out, nil
}

// recordUse marks the one-shot material a type handed out as used in the cache.
func (r *Runner) recordUse(g gen.Generator) {
	if c, ok := g.(gen.Consumer); ok && r.cache != nil {
		r.cache.MarkUsed(c.Consumed())
	}
}

func isShielded(name string) bool {
	_, transfer := gen.TransferActions(name)
	return transfer || name == gen.Shield || name == gen.Unshield
}

func describeStep(s profile.Step) string {
	if s.Mode == report.ModeOpen {
		return fmt.Sprintf("open-loop %g tx/s for %s", s.Rate, s.Duration)
	}
	return fmt.Sprintf("closed-loop window %d for %s", s.Window, s.Duration)
}

// skipped turns a preparation or calibration failure into a skipped type with its reason. A type
// that cannot be set up on this chain is a result; it is never replaced by something that looks like it.
func (r *Runner) skipped(t report.Type, err error) report.Type {
	reason, ok := gen.SkipReason(err)
	if ok && r.env.Shielded == nil && r.cacheReason != "" && isShielded(t.Name) {
		reason = r.cacheReason
	}
	if !ok {
		reason = fmt.Sprintf("setup failed: %v", err)
		r.warn("%s: %s", t.Name, reason)
	}
	t.Skipped, t.Reason = true, reason
	r.cfg.logf("%s: skipped: %s", t.Name, reason)
	return t
}

// calibrate sizes the gas limit of a type's transactions by simulating its first one, as the
// chain's own client does for a single transaction. A type whose transactions are all signerless
// declares its own gas and needs no simulation.
func (r *Runner) calibrate(ctx context.Context, g gen.Generator) (uint64, error) {
	if err := r.signer.reload(ctx); err != nil {
		return 0, err
	}
	p, err := g.Plan(0)
	if err != nil {
		return 0, fmt.Errorf("plan the first transaction: %w", err)
	}
	if p.Account == gen.Signerless {
		return p.Gas, nil
	}
	gas, err := r.cluster.Chain.EstimateGas(ctx, r.accounts[p.Account], p.Msg)
	if err != nil {
		return 0, gen.Skipf("the chain refuses a simulated transaction of this type: %v", err)
	}
	return gas, nil
}

// loadCache reads the shielded cache and checks it against the chain. A missing or stale cache is
// not an error: the shielded types are skipped with the reason.
func (r *Runner) loadCache(ctx context.Context) {
	if r.cfg.CachePath == "" {
		r.cacheReason = "no cache path was given"
		return
	}
	c, err := shielded.LoadCache(r.cfg.CachePath)
	if errors.Is(err, os.ErrNotExist) {
		r.cacheReason = fmt.Sprintf("there is no shielded cache at %s; run `oramad-bench prepare`", r.cfg.CachePath)
		return
	}
	if err != nil {
		r.cacheReason = err.Error()
		return
	}
	state, err := r.chainState(ctx)
	if err == nil {
		err = c.Validate(state, shielded.DefaultAnchorMargin)
	}
	if err != nil {
		r.cacheReason = fmt.Sprintf("the shielded cache is unusable (%v); run `oramad-bench prepare` again", err)
		return
	}
	r.cache, r.env.Shielded = c, c
}

// chainState is what the cache is checked against.
func (r *Runner) chainState(ctx context.Context) (shielded.ChainState, error) {
	sc := &shieldedChain{cluster: r.cluster, accounts: r.accounts, parallel: r.cfg.Parallel}
	genesis, err := sc.GenesisHash(ctx)
	if err != nil {
		return shielded.ChainState{}, err
	}
	tip, err := sc.Tip(ctx)
	if err != nil {
		return shielded.ChainState{}, err
	}
	return shielded.ChainState{ChainID: r.cluster.ChainID, GenesisHash: genesis, Height: tip.Height, TreeSize: tip.TreeSize}, nil
}

// saveCache writes the cache back with the bundles this run used marked used.
func (r *Runner) saveCache() {
	if r.cache == nil {
		return
	}
	if err := r.cache.Save(r.cfg.CachePath); err != nil {
		r.warn("could not save the shielded cache: %v", err)
	}
}
