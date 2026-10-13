package app

import (
	"context"
	"fmt"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	bankkeeper "github.com/cosmos/cosmos-sdk/x/bank/keeper"
	stakingkeeper "github.com/cosmos/cosmos-sdk/x/staking/keeper"

	"github.com/DeBrosOfficial/network/chain/app/params"
	housetypes "github.com/DeBrosOfficial/network/chain/x/houses/types"
	nodeskeeper "github.com/DeBrosOfficial/network/chain/x/nodes/keeper"
	powerkeeper "github.com/DeBrosOfficial/network/chain/x/power/keeper"
)

// houseStaking reads bonded stake for the token house. Amounts are norama,
// converted from shares, not the share count itself. Delegations lists only the delegations to
// bonded validators, the set whose tokens make up the bonded pool TotalBondedTokens reads: a
// delegation to a jailed or unbonding validator holds tokens that are not in that pool, and counting
// it would push the sum above the pool and fail every tally.
type houseStaking struct {
	staking *stakingkeeper.Keeper
	bank    bankkeeper.BaseKeeper
}

func (h houseStaking) TotalBondedTokens(ctx context.Context) (math.Int, error) {
	pool := h.staking.GetBondedPool(ctx)
	if pool == nil {
		return math.Int{}, fmt.Errorf("bonded pool is not created")
	}
	return h.bank.GetBalance(ctx, pool.GetAddress(), params.BaseDenom).Amount, nil
}

func (h houseStaking) Delegations(ctx context.Context) ([]housetypes.BondedDelegation, error) {
	dels, err := h.staking.GetAllDelegations(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]housetypes.BondedDelegation, 0, len(dels))
	for _, del := range dels {
		valAddr, err := sdk.ValAddressFromBech32(del.ValidatorAddress)
		if err != nil {
			return nil, err
		}
		val, err := h.staking.GetValidator(ctx, valAddr)
		if err != nil {
			return nil, err
		}
		if !val.IsBonded() {
			continue
		}
		delegator, err := sdk.AccAddressFromBech32(del.DelegatorAddress)
		if err != nil {
			return nil, err
		}
		out = append(out, housetypes.BondedDelegation{
			Delegator: delegator,
			Validator: sdk.AccAddress(valAddr),
			Amount:    val.TokensFromShares(del.Shares).TruncateInt(),
		})
	}
	return out, nil
}

// housePower reads the lambda x/power already stored. It does not recompute it.
type housePower struct {
	power powerkeeper.Keeper
}

func (h housePower) Lambda(ctx context.Context) (math.LegacyDec, error) {
	return h.power.Lambda.Get(ctx)
}

// houseOperators looks up x/nodes operators. An operator's Prefix16 and ASN come from
// its lowest-id ACTIVE node that has both (a /16 derivable from its endpoints and a
// declared ASN) and whose identity has stood unchanged for the network identity lock
// (x/nodes network_identity_lock_seconds); an operator with no such node keeps an
// empty prefix and a zero ASN, and x/houses skips it. The lock makes house
// eligibility count identity as of a rolling snapshot: a node that has just
// registered or changed its ASN or endpoints is not counted until the lock has
// passed, so identity cannot be moved to fit a vote. Both values are operator
// declarations, not verified on chain (docs/whitepaper/technical-reference/vol2/44-governance-and-contracts.md, "Node network identity").
//
// x/houses asks about the operators that hold a house bond only, and every read here is of that
// one operator: its record, its nodes through the by-operator index, and its service days up to the
// minimum the house requires.
type houseOperators struct {
	nodes nodeskeeper.Keeper
}

func (h houseOperators) Operator(ctx context.Context, address sdk.AccAddress) (housetypes.OperatorInfo, bool, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	addr := address.String()
	registered, err := h.nodes.Operators.Has(ctx, addr)
	if err != nil {
		return housetypes.OperatorInfo{}, false, fmt.Errorf("failed to read operator %s: %w", addr, err)
	}
	if !registered {
		return housetypes.OperatorInfo{}, false, nil
	}
	days, err := h.nodes.OperatorServiceDaysUpTo(sdkCtx, addr, housetypes.MinServiceDays)
	if err != nil {
		return housetypes.OperatorInfo{}, false, err
	}
	prefix, asn, err := h.nodes.OperatorNetwork(sdkCtx, addr)
	if err != nil {
		return housetypes.OperatorInfo{}, false, err
	}
	return housetypes.OperatorInfo{Address: address, Prefix16: prefix, ASN: asn, ServiceDays: days}, true, nil
}
