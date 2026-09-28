package keeper

import (
	"fmt"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/relay/types"
)

// Invariants is the result of the relay ceiling checks: minted never exceeds
// the ceiling recorded for that epoch, and the payout rows sum to minted.
type Invariants struct {
	CeilingHolds bool
	PayoutsMatch bool
	Detail       string
}

// CheckInvariants checks every settled epoch. A broken check is reported; the
// function itself returns an error only when state cannot be read.
func (k Keeper) CheckInvariants(ctx sdk.Context) (Invariants, error) {
	type row struct {
		ceiling math.Int
		minted  math.Int
		sum     math.Int
		seen    bool
	}
	rows := map[uint64]*row{}
	if err := k.EpochResults.Walk(ctx, nil, func(epoch uint64, result types.EpochResult) (bool, error) {
		rows[epoch] = &row{ceiling: result.Ceiling, minted: result.Minted, sum: math.ZeroInt(), seen: true}
		return false, nil
	}); err != nil {
		return Invariants{}, fmt.Errorf("failed to walk epoch results: %w", err)
	}
	if err := k.Payouts.Walk(ctx, nil, func(_ payoutMapKey, payout types.RelayPayout) (bool, error) {
		entry := rows[payout.Epoch]
		if entry == nil {
			entry = &row{sum: math.ZeroInt()}
			rows[payout.Epoch] = entry
		}
		entry.sum = entry.sum.Add(payout.Amount)
		return false, nil
	}); err != nil {
		return Invariants{}, fmt.Errorf("failed to walk payouts: %w", err)
	}

	ceilingHolds := true
	payoutsMatch := true
	detail := ""
	for epoch, entry := range rows {
		if !entry.seen || entry.ceiling.IsNil() || entry.minted.IsNil() || entry.minted.IsNegative() || entry.ceiling.IsNegative() || entry.minted.GT(entry.ceiling) {
			ceilingHolds = false
			detail += fmt.Sprintf("epoch %d minted %s ceiling %s\n", epoch, entry.minted, entry.ceiling)
		}
		minted := entry.minted
		if minted.IsNil() {
			minted = math.ZeroInt()
		}
		if !entry.seen || !entry.sum.Equal(minted) {
			payoutsMatch = false
			detail += fmt.Sprintf("epoch %d payouts %s minted %s\n", epoch, entry.sum, minted)
		}
	}
	if detail == "" {
		detail = fmt.Sprintf("ceiling holds: true\npayouts match: true\n%d settled epochs\n", len(rows))
	}
	return Invariants{CeilingHolds: ceilingHolds, PayoutsMatch: payoutsMatch, Detail: detail}, nil
}
