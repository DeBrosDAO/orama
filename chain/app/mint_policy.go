package app

import (
	"context"
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
)

// refuseNoramaMint is the minting restriction on x/token's bank keeper.
// x/token mints the denoms it creates; only x/emission mints norama.
func refuseNoramaMint(_ context.Context, coins sdk.Coins) error {
	if coins.AmountOf(params.BaseDenom).IsPositive() {
		return fmt.Errorf("only x/emission may mint %s", params.BaseDenom)
	}
	return nil
}
