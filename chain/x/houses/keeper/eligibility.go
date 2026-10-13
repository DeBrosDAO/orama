package keeper

import (
	"context"
	"fmt"
	"sort"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/houses/types"
)

type eligibleOperator struct {
	Address  string
	Prefix16 string
	ASN      uint32
}

// TierView is the coded opening check. It is not itself a vote.
type TierView struct {
	Parameter  bool
	Structural bool
	Lambda     math.LegacyDec
	Bonded     math.Int
	Eligible   int
	Prefix16s  int
	ASNs       int
}

// Tiers reports whether each tier is open.
func (k Keeper) Tiers(ctx context.Context) (TierView, error) {
	p, err := k.Params.Get(ctx)
	if err != nil {
		return TierView{}, fmt.Errorf("failed to load houses params: %w", err)
	}
	lambda, err := k.power.Lambda(ctx)
	if err != nil {
		return TierView{}, rejectAdvance(fmt.Errorf("failed to load lambda: %w", err))
	}
	if lambda.IsNil() {
		return TierView{}, rejectAdvance(fmt.Errorf("lambda is unset"))
	}
	bonded, err := k.staking.TotalBondedTokens(ctx)
	if err != nil {
		return TierView{}, rejectAdvance(fmt.Errorf("failed to load bonded stake: %w", err))
	}
	if bonded.IsNil() || bonded.IsNegative() {
		return TierView{}, rejectAdvance(fmt.Errorf("bonded stake must be a non-negative integer"))
	}
	eligible, err := k.eligibleSet(ctx)
	if err != nil {
		return TierView{}, err
	}
	prefixes, asns := diversity(eligible)
	stakeMet := !bonded.LT(p.BootstrapExitStake)
	lambdaMet := !lambda.LT(math.LegacyOneDec())
	houseMet := len(eligible) >= int(p.MinHouseSize)
	return TierView{
		Parameter:  (stakeMet || lambdaMet) && houseMet,
		Structural: lambdaMet && houseMet && prefixes >= types.MinDistinctPrefix16 && asns >= types.MinDistinctASN,
		Lambda:     lambda,
		Bonded:     bonded,
		Eligible:   len(eligible),
		Prefix16s:  prefixes,
		ASNs:       asns,
	}, nil
}

func (k Keeper) tierOpen(ctx context.Context, kind types.Kind) (bool, error) {
	view, err := k.Tiers(ctx)
	if err != nil {
		return false, err
	}
	switch kind {
	case types.KindParameter:
		return view.Parameter, nil
	case types.KindStructural:
		return view.Structural, nil
	default:
		return false, fmt.Errorf("proposal has no action")
	}
}

func (k Keeper) eligibleSet(ctx context.Context) (map[string]eligibleOperator, error) {
	p, err := k.Params.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load houses params: %w", err)
	}
	type candidate struct {
		eligibleOperator
		lockedAt int64
	}
	var cands []candidate
	// The walk is over the house bonds, not over the operators: an operator is eligible only with a
	// bond of at least HouseBond locked, so the walk is bounded by the stake locked in the house and
	// not by how many accounts registered as operators.
	err = k.Bonds.Walk(ctx, nil, func(address string, bond types.HouseBond) (bool, error) {
		if types.IntOrZero(bond.Amount).LT(p.HouseBond) {
			return false, nil
		}
		acc, err := sdk.AccAddressFromBech32(address)
		if err != nil {
			return true, fmt.Errorf("house bond address %s: %w", address, err)
		}
		op, found, err := k.operators.Operator(ctx, acc)
		if err != nil {
			return true, rejectAdvance(fmt.Errorf("failed to load operator %s: %w", address, err))
		}
		if !found || op.ServiceDays < types.MinServiceDays || op.Prefix16 == "" || op.ASN == 0 {
			return false, nil
		}
		cands = append(cands, candidate{
			eligibleOperator: eligibleOperator{Address: address, Prefix16: op.Prefix16, ASN: op.ASN},
			lockedAt:         bond.LockedAtHeight,
		})
		return false, nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list eligible operators: %w", err)
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].lockedAt != cands[j].lockedAt {
			return cands[i].lockedAt < cands[j].lockedAt
		}
		return cands[i].Address < cands[j].Address
	})
	prefixN := map[string]uint32{}
	asnN := map[uint32]uint32{}
	out := make(map[string]eligibleOperator, len(cands))
	for _, c := range cands {
		if prefixN[c.Prefix16] >= p.MaxEligiblePerPrefix16 || asnN[c.ASN] >= p.MaxEligiblePerAsn {
			continue
		}
		prefixN[c.Prefix16]++
		asnN[c.ASN]++
		out[c.Address] = c.eligibleOperator
	}
	return out, nil
}

func diversity(set map[string]eligibleOperator) (prefixes, asns int) {
	prefixSeen := map[string]struct{}{}
	asnSeen := map[uint32]struct{}{}
	// nosemgrep: consensus-map-iteration -- builds two sets and returns their sizes
	for _, op := range set {
		prefixSeen[op.Prefix16] = struct{}{}
		asnSeen[op.ASN] = struct{}{}
	}
	return len(prefixSeen), len(asnSeen)
}
