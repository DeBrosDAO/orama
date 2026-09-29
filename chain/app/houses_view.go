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
	nodestypes "github.com/DeBrosOfficial/network/chain/x/nodes/types"
	powerkeeper "github.com/DeBrosOfficial/network/chain/x/power/keeper"
)

// houseStaking reads bonded stake for the token house. Amounts are norama,
// converted from shares, not the share count itself.
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

// houseOperators lists x/nodes operators. An operator's Prefix16 and ASN come from
// its lowest-id ACTIVE node that has both (a /16 derivable from its endpoints and a
// declared ASN); an operator with no such node keeps an empty prefix and a zero ASN,
// and x/houses skips it. Both values are operator declarations, not verified on
// chain (docs/CHAIN.md, "Node network identity").
type houseOperators struct {
	nodes nodeskeeper.Keeper
}

type operatorNetwork struct {
	prefix16 string
	asn      uint32
}

// networks maps each operator to the network identity of its lowest-id active node
// that has both a derivable /16 and a declared ASN.
func (h houseOperators) networks(ctx context.Context) (map[string]operatorNetwork, error) {
	out := map[string]operatorNetwork{}
	err := h.nodes.Nodes.Walk(ctx, nil, func(_ string, node nodestypes.Node) (bool, error) {
		if node.Status != nodestypes.NodeStatusActive || node.Asn == 0 {
			return false, nil
		}
		if _, taken := out[node.Operator]; taken {
			return false, nil
		}
		if prefix := nodestypes.NetworkOf(node.Endpoints); prefix != "" {
			out[node.Operator] = operatorNetwork{prefix16: prefix, asn: node.Asn}
		}
		return false, nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to read operator networks: %w", err)
	}
	return out, nil
}

func (h houseOperators) Operators(ctx context.Context) ([]housetypes.OperatorInfo, error) {
	networks, err := h.networks(ctx)
	if err != nil {
		return nil, err
	}
	var out []housetypes.OperatorInfo
	err = h.nodes.Operators.Walk(ctx, nil, func(key string, op nodestypes.Operator) (bool, error) {
		addr := op.Address
		if addr == "" {
			addr = key
		}
		acc, err := sdk.AccAddressFromBech32(addr)
		if err != nil {
			return false, err
		}
		days, err := h.nodes.OperatorServiceDays(sdk.UnwrapSDKContext(ctx), addr)
		if err != nil {
			return false, err
		}
		net := networks[addr]
		out = append(out, housetypes.OperatorInfo{
			Address:     acc,
			Prefix16:    net.prefix16,
			ASN:         net.asn,
			ServiceDays: days,
		})
		return false, nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
