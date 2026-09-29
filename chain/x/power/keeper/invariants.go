package keeper

import (
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/x/power/types"
)

// CheckInvariants checks that x/power's module account holds no norama. It pulls an epoch's
// validator share from x/emission and pays all of it out inside the same call
// (DistributeEpochRewards), so a balance between blocks is a stranded amount. It returns a
// human-readable detail and whether the account is empty.
func (k Keeper) CheckInvariants(ctx sdk.Context) (string, bool) {
	balance := k.bankKeeper.GetBalance(ctx, authtypes.NewModuleAddress(types.ModuleName), params.BaseDenom).Amount
	empty := balance.IsZero()
	return fmt.Sprintf("power module account empty: %t (balance=%s)\n", empty, balance), empty
}
