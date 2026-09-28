package ante

import (
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/wasmpolicy/keeper"
)

// UploadSunsetDecorator rejects MsgStoreCode before upload_sunset_height unless the
// code id is in the genesis code set, and rejects any message that would change the height.
type UploadSunsetDecorator struct {
	keeper keeper.Keeper
}

// NewUploadSunsetDecorator returns the ante decorator for keeper.
func NewUploadSunsetDecorator(k keeper.Keeper) UploadSunsetDecorator {
	return UploadSunsetDecorator{keeper: k}
}

// AnteHandle applies the upload sunset to every message in tx. simulate is not exempt.
func (d UploadSunsetDecorator) AnteHandle(ctx sdk.Context, tx sdk.Tx, simulate bool, next sdk.AnteHandler) (sdk.Context, error) {
	for _, msg := range tx.GetMsgs() {
		if err := d.keeper.CheckMsg(ctx, ctx.BlockHeight(), msg); err != nil {
			return ctx, err
		}
	}
	return next(ctx, tx, simulate)
}
