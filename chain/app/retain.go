package app

import (
	"fmt"

	abci "github.com/cometbft/cometbft/abci/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

// Commit persists the block and returns CometBFT's retain height gated by x/archive (C14).
//
// BaseApp computes the retain height from min-retain-blocks, the evidence age and the snapshot
// interval, and returns 0 when the operator has not enabled pruning. This keeps 0 (prune
// nothing) and otherwise lowers the height to x/archive's own: min(tip - retention window, last
// contiguous archived height). A block that no committed range covers is never pruned, however
// far the tip runs ahead of a stalled archive, and while nothing is archived nothing is pruned.
func (app *OramaApp) Commit() (*abci.ResponseCommit, error) {
	resp, err := app.BaseApp.Commit()
	if err != nil {
		return nil, err
	}
	if resp.RetainHeight <= 0 {
		return resp, nil
	}
	height := app.LastBlockHeight()
	ctx := sdk.NewContext(app.CommitMultiStore(), cmtproto.Header{Height: height}, false, app.Logger())
	archived, err := app.ArchiveKeeper.RetainHeight(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read the archive retain height at block %d: %w", height, err)
	}
	resp.RetainHeight = gateRetainHeight(resp.RetainHeight, archived)
	return resp, nil
}

// gateRetainHeight is the lower of BaseApp's retain height and x/archive's. A non-positive
// archive retain height means nothing may be pruned, which CometBFT reads as 0.
func gateRetainHeight(base, archived int64) int64 {
	if base <= 0 || archived <= 0 {
		return 0
	}
	return min(base, archived)
}
