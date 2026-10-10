// Package keeper implements x/houses: two-house governance with coded tier
// gates, timelocks and a slashable operator bond (plans/open-network.md D17).
// It has no authority key, no pause and no expedited proposal.
package keeper

import (
	"context"
	"fmt"

	"cosmossdk.io/collections"
	storetypes "cosmossdk.io/core/store"
	"cosmossdk.io/log/v2"

	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/houses/types"
)

// Keeper is x/houses' keeper.
type Keeper struct {
	cdc       codec.BinaryCodec
	bank      types.BankKeeper
	staking   types.StakingKeeper
	power     types.PowerKeeper
	operators types.OperatorKeeper
	emission  types.EmissionKeeper
	earnings  types.EarningsKeeper
	reporters types.ReporterKeeper
	upgrades  types.UpgradeScheduler

	Schema         collections.Schema
	Params         collections.Item[types.Params]
	NextProposalID collections.Item[uint64]
	Proposals      collections.Map[uint64, types.Proposal]
	TokenVotes     collections.Map[collections.Pair[uint64, string], types.Vote]
	OperatorVotes  collections.Map[collections.Pair[uint64, string], types.Vote]
	Bonds          collections.Map[string, types.HouseBond]
	Active         collections.KeySet[uint64]
	Equivocations  collections.KeySet[collections.Pair[uint64, string]]
	Enacted        collections.Item[types.Enacted]
}

// NewKeeper builds a keeper. None of the keepers it depends on are x/power
// or x/nodes; those are interfaces supplied by the app.
func NewKeeper(
	cdc codec.BinaryCodec,
	storeService storetypes.KVStoreService,
	bank types.BankKeeper,
	staking types.StakingKeeper,
	power types.PowerKeeper,
	operators types.OperatorKeeper,
	emission types.EmissionKeeper,
	earnings types.EarningsKeeper,
) Keeper {
	sb := collections.NewSchemaBuilder(storeService)
	pairKey := collections.PairKeyCodec(collections.Uint64Key, collections.StringKey)
	k := Keeper{
		cdc:            cdc,
		bank:           bank,
		staking:        staking,
		power:          power,
		operators:      operators,
		emission:       emission,
		earnings:       earnings,
		Params:         collections.NewItem(sb, types.ParamsKey, "params", codec.CollValue[types.Params](cdc)),
		NextProposalID: collections.NewItem(sb, types.NextProposalIDKey, "next_proposal_id", collections.Uint64Value),
		Proposals:      collections.NewMap(sb, types.ProposalsPrefix, "proposals", collections.Uint64Key, codec.CollValue[types.Proposal](cdc)),
		TokenVotes:     collections.NewMap(sb, types.TokenVotesPrefix, "token_votes", pairKey, codec.CollValue[types.Vote](cdc)),
		OperatorVotes:  collections.NewMap(sb, types.OperatorVotesPrefix, "operator_votes", pairKey, codec.CollValue[types.Vote](cdc)),
		Bonds:          collections.NewMap(sb, types.BondsPrefix, "bonds", collections.StringKey, codec.CollValue[types.HouseBond](cdc)),
		Active:         collections.NewKeySet(sb, types.ActivePrefix, "active", collections.Uint64Key),
		Equivocations:  collections.NewKeySet(sb, types.EquivocationsPrefix, "equivocations", pairKey),
		Enacted:        collections.NewItem(sb, types.EnactedKey, "enacted", codec.CollValue[types.Enacted](cdc)),
	}
	schema, err := sb.Build()
	if err != nil {
		panic(err)
	}
	k.Schema = schema
	return k
}

// WithEnactors returns a copy of k that executes passed reporter changes and
// software upgrades through reporters and upgrades. Without them those two
// kinds of proposal fail at execution instead of recording an outcome nothing
// reads. Build the app module from the returned value.
func (k Keeper) WithEnactors(reporters types.ReporterKeeper, upgrades types.UpgradeScheduler) Keeper {
	k.reporters = reporters
	k.upgrades = upgrades
	return k
}

// Logger returns a module-specific logger.
func (k Keeper) Logger(ctx context.Context) log.Logger {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	return sdkCtx.Logger().With("module", "x/"+types.ModuleName)
}

func (k Keeper) getProposal(ctx context.Context, id uint64) (types.Proposal, error) {
	p, err := k.Proposals.Get(ctx, id)
	if err != nil {
		return types.Proposal{}, fmt.Errorf("failed to load proposal %d: %w", id, err)
	}
	return p, nil
}

func (k Keeper) activeIDs(ctx context.Context) ([]uint64, error) {
	var ids []uint64
	err := k.Active.Walk(ctx, nil, func(id uint64) (bool, error) {
		ids = append(ids, id)
		return false, nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to walk active proposals: %w", err)
	}
	return ids, nil
}
