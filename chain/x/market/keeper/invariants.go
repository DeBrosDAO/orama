package keeper

import (
	"context"
	"fmt"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"

	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/x/market/types"
)

// CheckInvariants checks bid escrow: the market module's norama balance is
// exactly the sum of open bids. A sale pays out in the same message that
// takes the payment, so nothing else stays in the account. It returns a
// human-readable detail and whether the invariant is broken.
func (k Keeper) CheckInvariants(ctx context.Context) (string, bool, error) {
	escrowed := math.ZeroInt()
	err := k.Bids.Walk(ctx, nil, func(_ collections.Pair[uint64, uint64], bid types.Bid) (bool, error) {
		escrowed = escrowed.Add(bid.Amount)
		return false, nil
	})
	if err != nil {
		return "", false, fmt.Errorf("walk bids: %w", err)
	}
	balance := k.bank.GetBalance(ctx, authtypes.NewModuleAddress(types.ModuleName), params.BaseDenom).Amount
	detail := fmt.Sprintf("market balance %s, open bids %s", balance, escrowed)
	return detail, !balance.Equal(escrowed), nil
}
