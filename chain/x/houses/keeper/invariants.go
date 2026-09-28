package keeper

import (
	"fmt"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/x/houses/types"
)

// HouseInvariants is the result of CheckInvariants.
type HouseInvariants struct {
	BondsMatchModule bool
	Detail           string
}

// CheckInvariants checks that the sum of locked house bonds equals the houses
// module account balance. Slashed bonds are burned, so they are in neither.
func (k Keeper) CheckInvariants(ctx sdk.Context) (HouseInvariants, error) {
	sum := math.ZeroInt()
	err := k.Bonds.Walk(ctx, nil, func(_ string, bond types.HouseBond) (bool, error) {
		sum = sum.Add(types.IntOrZero(bond.Amount))
		return false, nil
	})
	if err != nil {
		return HouseInvariants{}, fmt.Errorf("failed to sum house bonds: %w", err)
	}
	module := k.bank.GetBalance(ctx, authtypes.NewModuleAddress(types.ModuleName), params.BaseDenom).Amount
	match := sum.Equal(module)
	detail := fmt.Sprintf("bonds match module: %t (ledger=%s module=%s)\n", match, sum, module)
	return HouseInvariants{BondsMatchModule: match, Detail: detail}, nil
}
