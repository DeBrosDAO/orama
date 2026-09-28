package keeper

import (
	"context"
	"fmt"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"

	"github.com/DeBrosOfficial/network/chain/x/houses/types"
)

func (k Keeper) tallyToken(ctx context.Context, proposalID uint64) (yes, no, abstain math.Int, err error) {
	bonded, err := k.staking.TotalBondedTokens(ctx)
	if err != nil {
		return math.Int{}, math.Int{}, math.Int{}, fmt.Errorf("failed to load bonded stake: %w", err)
	}
	dels, err := k.staking.Delegations(ctx)
	if err != nil {
		return math.Int{}, math.Int{}, math.Int{}, fmt.Errorf("failed to load delegations: %w", err)
	}
	votes, err := k.voteOptions(ctx, k.TokenVotes, proposalID)
	if err != nil {
		return math.Int{}, math.Int{}, math.Int{}, err
	}
	return tallyTokenVotes(bonded, dels, votes)
}

// tallyTokenVotes applies the 3% cap to stake that inherits a validator's
// vote. A direct vote is removed from that validator's bucket and counted in
// full.
func tallyTokenVotes(bonded math.Int, dels []types.BondedDelegation, votes map[string]types.VoteOption) (yes, no, abstain math.Int, err error) {
	if bonded.IsNil() || bonded.IsNegative() {
		return math.Int{}, math.Int{}, math.Int{}, fmt.Errorf("bonded stake must be a non-negative integer")
	}
	yes, no, abstain = math.ZeroInt(), math.ZeroInt(), math.ZeroInt()
	cap := bonded.MulRaw(types.DelegatedVoteCapPercent).QuoRaw(100)
	inherited := map[string]math.Int{}
	inheritedOpt := map[string]types.VoteOption{}
	sum := math.ZeroInt()
	for _, d := range dels {
		if d.Delegator == nil || d.Validator == nil || d.Amount.IsNil() || d.Amount.IsNegative() {
			return math.Int{}, math.Int{}, math.Int{}, fmt.Errorf("delegation is incomplete")
		}
		sum = sum.Add(d.Amount)
		delegator := d.Delegator.String()
		validator := d.Validator.String()
		if opt, ok := votes[delegator]; ok {
			addVote(&yes, &no, &abstain, opt, d.Amount)
			continue
		}
		if delegator == validator {
			continue
		}
		opt, ok := votes[validator]
		if !ok {
			continue
		}
		if _, seen := inherited[validator]; !seen {
			inherited[validator] = math.ZeroInt()
		}
		inherited[validator] = inherited[validator].Add(d.Amount)
		inheritedOpt[validator] = opt
	}
	if sum.GT(bonded) {
		return math.Int{}, math.Int{}, math.Int{}, fmt.Errorf("delegations %s exceed bonded stake %s", sum, bonded)
	}
	for validator, amt := range inherited {
		counted := amt
		if counted.GT(cap) {
			counted = cap
		}
		addVote(&yes, &no, &abstain, inheritedOpt[validator], counted)
	}
	return yes, no, abstain, nil
}

func addVote(yes, no, abstain *math.Int, opt types.VoteOption, amt math.Int) {
	switch opt {
	case types.VoteOption_YES:
		*yes = yes.Add(amt)
	case types.VoteOption_NO:
		*no = no.Add(amt)
	case types.VoteOption_ABSTAIN:
		*abstain = abstain.Add(amt)
	}
}

func tokenHousePasses(yes, no, abstain, bonded math.Int, quorum, threshold math.LegacyDec) bool {
	if bonded.IsNil() || !bonded.IsPositive() || quorum.IsNil() || threshold.IsNil() {
		return false
	}
	participation := yes.Add(no).Add(abstain)
	if !participation.IsPositive() {
		return false
	}
	need := math.LegacyNewDecFromInt(bonded).Mul(quorum)
	if math.LegacyNewDecFromInt(participation).LT(need) {
		return false
	}
	if !yes.GT(no) {
		return false
	}
	share := math.LegacyNewDecFromInt(yes).Quo(math.LegacyNewDecFromInt(participation))
	return !share.LT(threshold)
}

func (k Keeper) tallyOperators(ctx context.Context, proposalID uint64) (yes, no, abstain, eligible uint64, err error) {
	set, err := k.eligibleSet(ctx)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	votes, err := k.voteOptions(ctx, k.OperatorVotes, proposalID)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	for addr := range set {
		equivocated, err := k.Equivocations.Has(ctx, collections.Join(proposalID, addr))
		if err != nil {
			return 0, 0, 0, 0, fmt.Errorf("failed to check equivocation for %s: %w", addr, err)
		}
		if equivocated {
			continue
		}
		switch votes[addr] {
		case types.VoteOption_YES:
			yes++
		case types.VoteOption_NO:
			no++
		case types.VoteOption_ABSTAIN:
			abstain++
		}
	}
	return yes, no, abstain, uint64(len(set)), nil
}

func operatorMajority(yes, eligible uint64) bool {
	if eligible == 0 {
		return false
	}
	return math.NewIntFromUint64(yes).MulRaw(2).GT(math.NewIntFromUint64(eligible))
}

func vetoed(no, eligible uint64) bool {
	if eligible == 0 {
		return true
	}
	return math.NewIntFromUint64(no).MulRaw(100).GTE(math.NewIntFromUint64(eligible).MulRaw(types.VetoPercent))
}

func (k Keeper) voteOptions(ctx context.Context, votes collections.Map[collections.Pair[uint64, string], types.Vote], proposalID uint64) (map[string]types.VoteOption, error) {
	out := map[string]types.VoteOption{}
	err := votes.Walk(ctx, collections.NewPrefixedPairRange[uint64, string](proposalID), func(key collections.Pair[uint64, string], vote types.Vote) (bool, error) {
		out[key.K2()] = vote.Option
		return false, nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to walk votes for proposal %d: %w", proposalID, err)
	}
	return out, nil
}
