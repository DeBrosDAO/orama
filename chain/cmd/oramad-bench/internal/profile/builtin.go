package profile

import (
	"time"

	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/gen"
	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/shielded"
)

// Smoke is the short profile `make bench-load` runs: every type once or twice at a rate any
// machine keeps up with. It shows that the pipeline works end to end and gives rough numbers; it
// does not find a saturation point, because its steps are too short and too few for that.
func Smoke() Profile {
	const short = 10 * time.Second
	return Profile{
		Name:     "smoke",
		Accounts: 16,
		Types: []TypePlan{
			{Type: gen.BankSend, Steps: []Step{open(20, short), closed(16, short)}},
			{Type: gen.TokenTransfer, Steps: []Step{open(20, short)}},
			{Type: gen.WasmCall, Steps: []Step{open(10, short)}},
			{Type: gen.StorageDeal, Steps: []Step{open(5, short)}},
			{Type: gen.RegisterNode, Steps: []Step{open(5, short)}},
			{Type: gen.CnftMint, Steps: []Step{open(10, short)}},
			{Type: gen.MarketList, Steps: []Step{open(10, short)}},
			{Type: gen.Shield, Steps: []Step{open(2, 2*time.Second)}},
			{Type: gen.Unshield, Steps: []Step{open(1, 4*time.Second)}},
			{Type: gen.ShieldedTransferPrefix + "2", Steps: []Step{open(1, 6*time.Second)}},
			{Type: gen.ShieldedTransferPrefix + "4", Steps: []Step{open(1, 3*time.Second)}},
			{Type: gen.ShieldedTransferPrefix + "8", Steps: []Step{open(1, time.Second)}},
		},
	}
}

// Full is the profile that looks for saturation: a ladder of open-loop rates and of closed-loop
// windows per type, each step long enough to be judged (see report.MinIntervalsToJudge). Shielded
// types climb a lower ladder because every transaction needs a proof made beforehand (seconds of CPU
// each, more for more actions), so the profile's size is bounded by how long you are willing to wait
// for `prepare`.
func Full() Profile {
	const long = 30 * time.Second
	cheap := []Step{open(10, long), open(25, long), open(50, long), open(100, long), open(200, long), open(400, long), open(800, long), open(1600, long), closed(8, long), closed(32, long), closed(128, long), closed(512, long), closed(2048, long)}
	mid := []Step{open(5, long), open(10, long), open(25, long), open(50, long), open(100, long), closed(16, long), closed(64, long), closed(256, long)}
	proved := func(rates ...float64) []Step { // the same 30 s as every other step: a shorter one is not judged
		steps := make([]Step, len(rates))
		for i, r := range rates {
			steps[i] = open(r, long)
		}
		return steps
	}
	return Profile{
		Name:     "full",
		Accounts: 64,
		Types: []TypePlan{
			{Type: gen.BankSend, Steps: cheap},
			{Type: gen.TokenTransfer, Steps: cheap},
			{Type: gen.WasmCall, Steps: mid},
			{Type: gen.StorageDeal, Steps: mid},
			{Type: gen.RegisterNode, Steps: mid},
			{Type: gen.CnftMint, Steps: cheap},
			{Type: gen.MarketList, Steps: mid},
			{Type: gen.Shield, Steps: proved(2, 4, 8)},
			{Type: gen.Unshield, Steps: proved(1, 2, 4)},
			{Type: gen.ShieldedTransferPrefix + "2", Steps: proved(1, 2, 4, 8)},
			{Type: gen.ShieldedTransferPrefix + "4", Steps: proved(1, 2, 4)},
			{Type: gen.ShieldedTransferPrefix + "8", Steps: proved(1, 2)},
			{Type: gen.ShieldedTransferPrefix + "16", Steps: proved(0.5, 1)},
		},
	}
}

// ShieldedPlan is the bundles the profile's shielded types need, plus one spare of each so a probe
// or an off-by-one at the end of a step never runs the cache dry.
func (p Profile) ShieldedPlan(signers int) shielded.Plan {
	plan := shielded.Plan{Transfers: map[int]int{}, Signers: signers}
	for _, t := range p.Types {
		n := t.TotalTxs() + 1
		if actions, ok := gen.TransferActions(t.Type); ok {
			plan.Transfers[actions] = n
			continue
		}
		switch t.Type {
		case gen.Shield:
			plan.Shields = n
		case gen.Unshield:
			plan.Unshields = n
		}
	}
	return plan
}
