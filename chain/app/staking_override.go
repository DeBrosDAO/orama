package app

import (
	"context"
	"encoding/json"

	abci "github.com/cometbft/cometbft/abci/types"

	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"
	"github.com/cosmos/cosmos-sdk/x/staking"
	stakingkeeper "github.com/cosmos/cosmos-sdk/x/staking/keeper"
)

// stakingEndBlockOverride wraps staking.AppModule so its EndBlock still runs x/staking's own
// bookkeeping (maturing unbonding/redelegation queues, and the internal bonded/unbonded status
// transitions and pool accounting that come with it) but never returns its computed
// []abci.ValidatorUpdate to CometBFT (plans/open-network/track-c-chain.md C4: "The CometBFT
// validator set comes from x/power, not stock x/staking's EndBlocker updates").
//
// x/staking still owns bonds and delegations - MsgCreateValidator, MsgDelegate, MsgUndelegate and
// slashing/jailing all work completely unmodified. Only the question "what voting power does
// CometBFT actually see" is answered elsewhere, by x/power's own EndBlock
// (power.EndBlocker -> power/keeper.Keeper.RunEndBlock), which is the sole
// module.HasABCIEndBlock in this app's end-blocker order that ever returns a non-empty update
// list (the SDK's module manager errors if two modules both do - see
// types/module.Manager.EndBlock).
//
// It also registers x/staking's Msg service wrapped so that a signer's own bond shortfall is
// funded from their earnings while MsgCreateValidator or MsgDelegate executes (see
// earningsFundedStaking).
type stakingEndBlockOverride struct {
	staking.AppModule

	stakingKeeper *stakingkeeper.Keeper
	funder        earningsFunder
}

// newStakingEndBlockOverride wraps am, using keeper's own EndBlocker for the discarded updates and
// funder for the bond top-up.
func newStakingEndBlockOverride(am staking.AppModule, keeper *stakingkeeper.Keeper, funder earningsFunder) stakingEndBlockOverride {
	return stakingEndBlockOverride{AppModule: am, stakingKeeper: keeper, funder: funder}
}

// RegisterServices registers x/staking's services unchanged except that the Msg service is
// wrapped by earningsFundedStaking.
func (w stakingEndBlockOverride) RegisterServices(cfg module.Configurator) {
	w.AppModule.RegisterServices(earningsFundedConfigurator{Configurator: cfg, funder: w.funder})
}

// EndBlock runs x/staking's own end-blocker for its bonding/unbonding side effects, and always
// returns no validator updates.
func (w stakingEndBlockOverride) EndBlock(ctx context.Context) ([]abci.ValidatorUpdate, error) {
	if _, err := w.stakingKeeper.EndBlocker(ctx); err != nil {
		return nil, err
	}
	return nil, nil
}

// InitGenesis runs x/staking's own genesis initialization (validators, pools, delegations - all of
// it, unchanged) but always returns no validator updates, exactly like EndBlock above.
//
// Security review B6: on a genesis produced by ExportGenesis to continue an existing chain, x/staking's
// own exported Validators list is no longer empty - it contains every validator that has ever
// bonded, including former bootstrap committee members with their real accumulated tokens - so
// staking's stock InitGenesis returns a non-empty validator-update list for them. x/power's own
// InitGenesis, which runs after staking's, ALSO returns a non-empty list (built from the imported
// power_records - see power/keeper.Keeper.InitGenesis). Since the SDK's module manager errors if two
// modules both return non-empty InitGenesis validator updates ("validator InitGenesis updates
// already set by a previous module"), staking's must always be discarded here, on every genesis
// (fresh or exported) - not just in EndBlock.
func (w stakingEndBlockOverride) InitGenesis(ctx sdk.Context, cdc codec.JSONCodec, data json.RawMessage) []abci.ValidatorUpdate {
	w.AppModule.InitGenesis(ctx, cdc, data)
	return nil
}
