// Package keeper implements x/power's state machine: the hand-over factor lambda, the capped
// stake share with its cap hysteresis, the per-validator ramp, the bootstrap committee, and the
// CometBFT validator updates the app returns instead of x/staking's own
// (plans/open-network/track-c-chain.md C4).
package keeper

import (
	"context"

	"cosmossdk.io/collections"
	storetypes "cosmossdk.io/core/store"
	"cosmossdk.io/log/v2"
	"cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/power/types"
)

// Keeper is x/power's keeper. It has no authority address and no Msg service: bootstrap committee
// membership and the power formula's parameters are genesis-only, and nothing can change lambda,
// the cap or the ramp except x/power's own EndBlock (plans/open-network.md D18).
type Keeper struct {
	storeService   storetypes.KVStoreService
	stakingKeeper  types.StakingKeeper
	slashingKeeper types.SlashingKeeper
	bankKeeper     types.BankKeeper
	earningsKeeper types.EarningsKeeper
	// operators names the operator a validator's consensus key belongs to. Nil means there is no
	// operator registry, and every validator is its own operator.
	operators types.OperatorResolver

	Schema collections.Schema
	Params collections.Item[types.Params]

	// Lambda is the current hand-over factor (types.ComputeLambda). LambdaLastUpdatedEpoch is the
	// x/emission epoch it was last recomputed for, so RunEpoch never recomputes it twice for the
	// same epoch close.
	Lambda                 collections.Item[math.LegacyDec]
	LambdaLastUpdatedEpoch collections.Item[uint64]

	// GateSatisfied is a one-way ratchet (security review H3(a)): once the number of consensus
	// participants (bonded validators plus eligible committee members) has reached
	// types.HandoverGateThreshold at least once, this is set to true forever, and lambda is no
	// longer held at Params.PreGateLambdaCap.
	GateSatisfied collections.Item[bool]

	// CapCurrentBps and CapBelowStreak are types.CapState's two fields, stored separately since
	// CapState is a plain Go struct with no proto codec of its own (see types.CapState's doc
	// comment).
	CapCurrentBps  collections.Item[uint64]
	CapBelowStreak collections.Item[uint64]

	// GenesisEpoch is the x/emission epoch number in progress when this chain incarnation's
	// genesis ran; lambda's time-based term measures epochs_since_genesis from it.
	GenesisEpoch collections.Item[uint64]

	// BootstrapCommittee is the fixed genesis committee, keyed by operator (bech32 account)
	// address. No Msg service ever writes to it after InitGenesis.
	BootstrapCommittee collections.Map[string, types.BootstrapMember]

	// RampActivation records, per validator operator address, the epoch its capped stake share
	// first became positive - the start of its types.RampFactor ramp. Set once, never updated.
	RampActivation collections.Map[string, uint64]

	// LastPower records, per validator operator address, the CometBFT integer power x/power last
	// assigned it, so EndBlock's diff only emits a ValidatorUpdate for what actually changed.
	LastPower collections.Map[string, int64]

	// LastPubKey records, per validator operator address, the raw consensus pubkey bytes last used
	// in a ValidatorUpdate, so a removal update (power 0) can reuse the exact identity CometBFT
	// already has on file even after the source (a committee record or a staking validator) is
	// gone or has changed.
	LastPubKey collections.Map[string, []byte]

	// CommitteeSelfBond records, per committee member operator address, how much norama x/power
	// has force-bonded into their self-delegation so far (Params.ForceBondFraction), so the
	// SelfBondCapMultiplier ceiling can be checked without re-deriving it from delegation history.
	CommitteeSelfBond collections.Map[string, math.Int]

	// RampAdmitted is the bonded-token amount that has finished its ramp.
	// RampExcess is the later increase still ramping, from RampExcessEpoch.
	// A stake increase does not become voting power all at once.
	RampAdmitted    collections.Map[string, math.Int]
	RampExcess      collections.Map[string, math.Int]
	RampExcessEpoch collections.Map[string, uint64]
}

// NewKeeper builds a new x/power Keeper.
func NewKeeper(
	cdc codec.BinaryCodec,
	storeService storetypes.KVStoreService,
	stakingKeeper types.StakingKeeper,
	slashingKeeper types.SlashingKeeper,
	bankKeeper types.BankKeeper,
	earningsKeeper types.EarningsKeeper,
) Keeper {
	sb := collections.NewSchemaBuilder(storeService)
	k := Keeper{
		storeService:           storeService,
		stakingKeeper:          stakingKeeper,
		slashingKeeper:         slashingKeeper,
		bankKeeper:             bankKeeper,
		earningsKeeper:         earningsKeeper,
		Params:                 collections.NewItem(sb, types.ParamsKey, "params", codec.CollValue[types.Params](cdc)),
		Lambda:                 collections.NewItem(sb, types.LambdaStateKey, "lambda", sdk.LegacyDecValue),
		LambdaLastUpdatedEpoch: collections.NewItem(sb, collections.NewPrefix(10), "lambda_last_updated_epoch", collections.Uint64Value),
		GateSatisfied:          collections.NewItem(sb, collections.NewPrefix(13), "gate_satisfied", collections.BoolValue),
		CapCurrentBps:          collections.NewItem(sb, types.CapStateKey, "cap_current_bps", collections.Uint64Value),
		CapBelowStreak:         collections.NewItem(sb, collections.NewPrefix(11), "cap_below_streak", collections.Uint64Value),
		GenesisEpoch:           collections.NewItem(sb, collections.NewPrefix(12), "genesis_epoch", collections.Uint64Value),
		BootstrapCommittee:     collections.NewMap(sb, types.BootstrapCommitteePrefix, "bootstrap_committee", collections.StringKey, codec.CollValue[types.BootstrapMember](cdc)),
		RampActivation:         collections.NewMap(sb, types.RampRecordsPrefix, "ramp_activation", collections.StringKey, collections.Uint64Value),
		LastPower:              collections.NewMap(sb, types.LastPowerPrefix, "last_power", collections.StringKey, collections.Int64Value),
		LastPubKey:             collections.NewMap(sb, types.LastPubKeyPrefix, "last_pub_key", collections.StringKey, collections.BytesValue),
		CommitteeSelfBond:      collections.NewMap(sb, types.CommitteeSelfBondPrefix, "committee_self_bond", collections.StringKey, sdk.IntValue),
		RampAdmitted:           collections.NewMap(sb, types.RampAdmittedPrefix, "ramp_admitted", collections.StringKey, sdk.IntValue),
		RampExcess:             collections.NewMap(sb, types.RampExcessPrefix, "ramp_excess", collections.StringKey, sdk.IntValue),
		RampExcessEpoch:        collections.NewMap(sb, types.RampExcessEpochPrefix, "ramp_excess_epoch", collections.StringKey, collections.Uint64Value),
	}

	schema, err := sb.Build()
	if err != nil {
		panic(err)
	}
	k.Schema = schema

	return k
}

// WithOperators returns a copy of the keeper that caps voting power per operator, as named by
// resolver, instead of per validator.
func (k Keeper) WithOperators(resolver types.OperatorResolver) Keeper {
	k.operators = resolver
	return k
}

// Logger returns a module-specific logger.
func (k Keeper) Logger(ctx context.Context) log.Logger {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	return sdkCtx.Logger().With("module", "x/"+types.ModuleName)
}
