package shielded

import (
	"errors"
	"fmt"
	"sort"

	"cosmossdk.io/math"
)

const (
	// Wallets is how many Orchard wallet accounts the bench uses. Notes are spread over them so a
	// transfer sends to a wallet other than its own.
	Wallets = 4
	// NoteValue is what each seed note holds, in norama: enough to pay the fee of the biggest
	// transfer from the notes it spends.
	NoteValue uint64 = 100_000_000
	// ShieldValue is what a measured shield puts into the pool, in norama.
	ShieldValue uint64 = 10_000_000
	// UnshieldAmount is what a measured unshield takes out, in norama: above two nullifier fees at
	// the default fee, below the default cap on a fee top-up.
	UnshieldAmount uint64 = 5_000_000
	// SeedOutputsPerBundle is how many seed notes one shield bundle creates.
	SeedOutputsPerBundle = 8
	// MinActions is the fewest actions a bundle has: Orchard pads a smaller one up to two.
	MinActions = 2

	// addressChange and addressPayment are the address indexes of a wallet that receive change and
	// a payment.
	addressChange  = 1
	addressPayment = 0
)

// Params are the x/shielded parameters a plan depends on.
type Params struct {
	ActionGas    uint64
	NullifierFee math.Int
	MaxActions   uint32
	MaxFeeTopup  math.Int
	AnchorWindow uint64
}

// Plan says how many bundles of each kind to build.
type Plan struct {
	// Shields is how many measured shield bundles (two actions each).
	Shields int
	// Transfers maps an action count to how many transfers of that size.
	Transfers map[int]int
	// Unshields is how many measured unshield bundles (two actions each).
	Unshields int
	// Signers is how many bench accounts there are; signed entries are spread over them.
	Signers int
}

// Validate checks the plan against the chain's limits.
func (p Plan) Validate(params Params) error {
	if p.Signers < 1 {
		return errors.New("a plan needs at least one signer account")
	}
	if p.Shields < 0 || p.Unshields < 0 {
		return errors.New("bundle counts cannot be negative")
	}
	for actions, n := range p.Transfers {
		if actions < MinActions || uint32(actions) > params.MaxActions {
			return fmt.Errorf("a transfer of %d actions is outside the chain's range [%d, %d]", actions, MinActions, params.MaxActions)
		}
		if n < 0 {
			return fmt.Errorf("transfers of %d actions: negative count", actions)
		}
	}
	if p.Unshields > 0 {
		floor := params.NullifierFee.MulRaw(MinActions)
		cap := params.MaxFeeTopup
		amount := math.NewIntFromUint64(UnshieldAmount)
		if amount.LTE(floor) || amount.GT(cap) {
			return fmt.Errorf("an unshield of %s is outside (%s, %s]: the chain's nullifier fees and fee top-up cap leave no room", amount, floor, cap)
		}
	}
	return nil
}

// SeedNotes is how many notes the spends of the plan consume.
func (p Plan) SeedNotes() int {
	n := p.Unshields
	for actions, count := range p.Transfers {
		n += actions * count
	}
	return n
}

// TransferFee is the value balance a transfer of the given actions must leave: the base fee at
// price per gas for its gas, plus one nullifier fee per action, plus one so rounding never leaves
// it short.
func TransferFee(price math.Int, params Params, actions int) math.Int {
	a := math.NewInt(int64(actions))
	gas := a.Mul(math.NewIntFromUint64(params.ActionGas))
	return price.Mul(gas).Add(params.NullifierFee.Mul(a)).AddRaw(1)
}

// SpendJobs is one job per bundle of the plan, taking its notes from the pools, which hold each
// wallet's notes (serialized). Transfers go first, largest first, then unshields. It fails when a
// pool runs out, which means the seed was sized wrong.
func (p Plan) SpendJobs(pools map[uint32][]string, price math.Int, params Params, signerAddr func(signer int) (string, error)) ([]SpendJob, error) {
	var jobs []SpendJob
	sizes := make([]int, 0, len(p.Transfers))
	for actions := range p.Transfers {
		sizes = append(sizes, actions)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(sizes)))
	next := 0
	take := func(owner uint32, n int) ([]SpendRef, error) {
		pool := pools[owner]
		if len(pool) < n {
			return nil, fmt.Errorf("wallet %d has %d seed notes left, a job needs %d", owner, len(pool), n)
		}
		refs := make([]SpendRef, n)
		for i := range refs {
			refs[i] = SpendRef{Account: owner, Note: pool[i]}
		}
		pools[owner] = pool[n:]
		return refs, nil
	}
	for _, actions := range sizes {
		for i := 0; i < p.Transfers[actions]; i++ {
			job, err := transferJob(fmt.Sprintf("transfer-%d-%d", actions, i), uint32(next%Wallets), actions, take, TransferFee(price, params, actions))
			if err != nil {
				return nil, err
			}
			jobs = append(jobs, job)
			next++
		}
	}
	for i := 0; i < p.Unshields; i++ {
		signer := i % p.Signers
		addr, err := signerAddr(signer)
		if err != nil {
			return nil, err
		}
		job, err := unshieldJob(fmt.Sprintf("unshield-%d", i), uint32(next%Wallets), addr, take)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
		next++
	}
	return jobs, nil
}

type takeFunc func(owner uint32, n int) ([]SpendRef, error)

// transferJob spends actions notes of one wallet: one note's worth goes to the next wallet, the
// rest less the fee comes back as change.
func transferJob(id string, owner uint32, actions int, take takeFunc, fee math.Int) (SpendJob, error) {
	spends, err := take(owner, actions)
	if err != nil {
		return SpendJob{}, err
	}
	total := math.NewIntFromUint64(NoteValue).MulRaw(int64(actions))
	change := total.SubRaw(int64(NoteValue)).Sub(fee)
	if !change.IsPositive() || !change.IsUint64() {
		return SpendJob{}, fmt.Errorf("%s: the fee %s leaves no change from %s", id, fee, total)
	}
	return SpendJob{
		ID: id, Kind: KindTransfer, Spends: spends,
		Outputs: []OutputSpec{
			{Account: (owner + 1) % Wallets, Address: addressPayment, Value: NoteValue},
			{Account: owner, Address: addressChange, Value: change.Uint64()},
		},
	}, nil
}

// unshieldJob spends one note: UnshieldAmount leaves the pool for signerAddr's fee-only balance
// and the rest comes back as change.
func unshieldJob(id string, owner uint32, signerAddrHex string, take takeFunc) (SpendJob, error) {
	spends, err := take(owner, 1)
	if err != nil {
		return SpendJob{}, err
	}
	return SpendJob{
		ID: id, Kind: KindUnshield, Spends: spends, UnshieldSigner: signerAddrHex,
		Outputs: []OutputSpec{{Account: owner, Address: addressChange, Value: NoteValue - UnshieldAmount}},
	}, nil
}

// SeedJobs is the shield bundles that create the seed notes: SeedOutputsPerBundle notes to a
// bundle, each wallet getting the notes its jobs will spend.
func (p Plan) SeedJobs() []ShieldJob {
	need := make([]int, Wallets)
	next := 0
	assign := func(n int) {
		need[next%Wallets] += n
		next++
	}
	sizes := make([]int, 0, len(p.Transfers))
	for actions := range p.Transfers {
		sizes = append(sizes, actions)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(sizes)))
	for _, actions := range sizes {
		for i := 0; i < p.Transfers[actions]; i++ {
			assign(actions)
		}
	}
	for i := 0; i < p.Unshields; i++ {
		assign(1)
	}
	var jobs []ShieldJob
	for wallet := uint32(0); wallet < Wallets; wallet++ {
		for left := need[wallet]; left > 0; left -= SeedOutputsPerBundle {
			n := min(left, SeedOutputsPerBundle)
			outs := make([]OutputSpec, n)
			for i := range outs {
				outs[i] = OutputSpec{Account: wallet, Address: addressPayment, Value: NoteValue}
			}
			jobs = append(jobs, ShieldJob{ID: fmt.Sprintf("seed-%d-%d", wallet, len(jobs)), Outputs: outs})
		}
	}
	return jobs
}
