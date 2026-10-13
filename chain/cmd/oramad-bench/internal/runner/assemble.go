package runner

import (
	"context"
	"runtime"
	"time"

	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/apphash"
	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/report"
)

// compareHeaders compares every node's header at every height in [from, to].
func compareHeaders(ctx context.Context, c *Cluster, from, to int64) (apphash.Result, error) {
	return apphash.CompareRange(ctx, c.Addrs, from, to, c.header)
}

// assemble puts the report together once the run is over.
func (r *Runner) assemble(started time.Time, types []report.Type, cmp apphash.Result, endHeight int64) (report.Report, error) {
	validators, err := r.cluster.Clients[0].ValidatorAddresses(context.Background(), endHeight)
	if err != nil {
		return report.Report{}, err
	}
	blocks := r.follower.Blocks(started, time.Now())
	rep := report.Report{
		Tool: report.Tool, StartedAt: started, FinishedAt: time.Now(), Profile: r.cfg.Profile.Name,
		Environment: report.Environment{GoVersion: runtime.Version(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, CPUs: runtime.NumCPU(), Note: EnvironmentNote},
		Chain: report.Chain{
			ChainID: r.cluster.ChainID, Nodes: r.cluster.Addrs, Validators: len(validators),
			VoteExtensionsEnableHeight: r.veHeight, VoteExtensionsOn: r.veHeight > 0 && r.veHeight <= endHeight,
			BlockMaxGas: r.blockMaxGas, StartHeight: r.startHeight + 1, EndHeight: endHeight,
			BroadcastMode: string(r.cfg.Broadcast), FeeMultiple: r.cfg.FeeMultiple, Accounts: len(r.accounts),
			BlockIntervalSeconds: report.BuildBlocks(blocks, r.blockMaxGas).IntervalSeconds,
		},
		Types: types, AppHash: cmp, AppHashAgreed: cmp.Check() == nil,
	}
	r.mu.Lock()
	rep.Warnings = append(rep.Warnings, r.warnings...)
	r.mu.Unlock()
	if failed := r.sampler.Failed(); failed > 0 {
		rep.Warnings = append(rep.Warnings, "some mempool readings failed; the mempool series has gaps")
	}
	return rep, nil
}
