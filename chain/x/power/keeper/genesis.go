package keeper

import (
	"fmt"
	"strings"

	abci "github.com/cometbft/cometbft/abci/types"

	"cosmossdk.io/math"
	cryptocodec "github.com/cosmos/cosmos-sdk/crypto/codec"
	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/DeBrosOfficial/network/chain/x/power/types"
)

// nonProductionChainIDMarkers mirrors x/emission's own gate (keeper.checkBootstrapChainID there):
// a mainnet chain-id contains none of these.
var nonProductionChainIDMarkers = []string{"-stagenet-", "-devnet-", "-localnet-"}

// InitGenesis sets x/power's state from a GenesisState and returns the genesis CometBFT
// validator set.
//
// On a true fresh genesis (genState.Exported == false), lambda is 0, so P_i == BootstrapShare_i for
// every committee member and 0 for everyone else - there is no other validator yet, since a real
// (self-bonded) validator can only be created after genesis, once an account holds norama to bond -
// and InitGenesis creates each committee member's staking validator record directly, and reads
// genesis_epoch from x/emission's own current epoch (so it must run after x/emission's InitGenesis).
//
// On an exported genesis (continuing an existing chain, e.g. across a coordinated hard fork -
// security review B6), InitGenesis instead: trusts the given genesis_epoch and lambda/cap state
// as-is; never recreates a committee member's staking validator record (x/staking's own InitGenesis,
// which always runs first, already restored every validator - including former committee members -
// from its own exported Validators list, with their real accumulated tokens; creating a fresh
// zero-token record over that would erase real force-bonded stake); and returns ValidatorUpdates
// built directly from the imported power_records rather than recomputing genesis equal-bootstrap
// shares, which describe only a true fresh genesis.
func (k Keeper) InitGenesis(ctx sdk.Context, genState types.GenesisState, emissionKeeper types.EmissionKeeper) ([]abci.ValidatorUpdate, error) {
	if err := genState.Validate(); err != nil {
		return nil, fmt.Errorf("invalid power genesis state: %w", err)
	}
	if err := checkCommitteeSizeChainIDGate(ctx, genState.Params, len(genState.BootstrapCommittee)); err != nil {
		return nil, err
	}

	if err := k.Params.Set(ctx, genState.Params); err != nil {
		return nil, fmt.Errorf("failed to set power params: %w", err)
	}
	if err := k.Lambda.Set(ctx, genState.Lambda); err != nil {
		return nil, fmt.Errorf("failed to set power lambda: %w", err)
	}
	if err := k.LambdaLastUpdatedEpoch.Set(ctx, genState.LambdaLastUpdatedEpoch); err != nil {
		return nil, fmt.Errorf("failed to set power lambda_last_updated_epoch: %w", err)
	}
	if err := k.CapCurrentBps.Set(ctx, genState.CurrentCapBps); err != nil {
		return nil, fmt.Errorf("failed to set power current_cap_bps: %w", err)
	}
	if err := k.CapBelowStreak.Set(ctx, genState.BelowStepUpStreakEpochs); err != nil {
		return nil, fmt.Errorf("failed to set power below_step_up_streak_epochs: %w", err)
	}
	if err := k.GateSatisfied.Set(ctx, genState.GateSatisfied); err != nil {
		return nil, fmt.Errorf("failed to set power gate_satisfied: %w", err)
	}

	genesisEpoch := genState.GenesisEpoch
	if !genState.Exported {
		currentEmissionEpoch, err := emissionKeeper.CurrentEpoch(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to read x/emission's current epoch for power genesis_epoch: %w", err)
		}
		genesisEpoch = currentEmissionEpoch
	}
	if err := k.GenesisEpoch.Set(ctx, genesisEpoch); err != nil {
		return nil, fmt.Errorf("failed to set power genesis_epoch: %w", err)
	}

	for _, member := range genState.BootstrapCommittee {
		valAddrStr, err := valoperFromAccountBech32(member.OperatorAddress)
		if err != nil {
			return nil, fmt.Errorf("invalid bootstrap committee operator_address %q: %w", member.OperatorAddress, err)
		}
		if err := k.BootstrapCommittee.Set(ctx, valAddrStr, member); err != nil {
			return nil, fmt.Errorf("failed to set bootstrap committee member %q: %w", member.OperatorAddress, err)
		}
	}
	for _, r := range genState.RampRecords {
		if err := k.importRampRecord(ctx, r); err != nil {
			return nil, err
		}
	}
	for _, r := range genState.PowerRecords {
		if err := k.LastPower.Set(ctx, r.OperatorAddress, r.CometPower); err != nil {
			return nil, fmt.Errorf("failed to set power record for %q: %w", r.OperatorAddress, err)
		}
		if len(r.ConsensusPubkey) > 0 {
			if err := k.LastPubKey.Set(ctx, r.OperatorAddress, r.ConsensusPubkey); err != nil {
				return nil, fmt.Errorf("failed to set power pubkey record for %q: %w", r.OperatorAddress, err)
			}
		}
	}
	for _, r := range genState.CommitteeSelfBonds {
		if err := k.CommitteeSelfBond.Set(ctx, r.OperatorAddress, r.ForceBonded); err != nil {
			return nil, fmt.Errorf("failed to set committee self-bond record for %q: %w", r.OperatorAddress, err)
		}
	}

	if genState.Exported {
		return validatorUpdatesFromPowerRecords(genState.PowerRecords)
	}
	return k.initGenesisCommitteeValidators(ctx, genState)
}

// initGenesisCommitteeValidators runs only on a true fresh genesis: it gives every bootstrap
// committee member a real (Bonded, zero-token) stakingtypes.Validator record - required for
// x/slashing's BeginBlocker (IsValidatorJailed -> GetValidatorByConsAddr) to run at all for it; see
// types.StakingKeeper's doc comment on SetValidator - and returns the equal-bootstrap-share genesis
// ValidatorUpdate for each. It is deliberately never indexed by power (no SetValidatorByPowerIndex
// call), so GetBondedValidatorsByPower - the "outsider" universe RunEndBlock/computePowers builds
// C_i from - never returns it on the strength of its bootstrap seat alone; only a real delegation
// (e.g. from force-bonded rewards) gives it any C_i.
func (k Keeper) initGenesisCommitteeValidators(ctx sdk.Context, genState types.GenesisState) ([]abci.ValidatorUpdate, error) {
	bootstrapShares := types.EqualBootstrapShares(len(genState.BootstrapCommittee))
	updates := make([]abci.ValidatorUpdate, 0, len(genState.BootstrapCommittee))
	for i, member := range genState.BootstrapCommittee {
		valAddrStr, err := valoperFromAccountBech32(member.OperatorAddress)
		if err != nil {
			return nil, fmt.Errorf("invalid bootstrap committee operator_address %q: %w", member.OperatorAddress, err)
		}
		cometPower := types.PowerToCometBFT(bootstrapShares[i], genState.Params.CometPowerScale)
		pubKey := &ed25519.PubKey{Key: append([]byte(nil), member.ConsensusPubkey...)}
		pkProto, err := cryptocodec.ToCmtProtoPublicKey(pubKey)
		if err != nil {
			return nil, fmt.Errorf("failed to encode consensus pubkey for %q: %w", member.OperatorAddress, err)
		}

		validator, err := stakingtypes.NewValidator(valAddrStr, pubKey, stakingtypes.Description{Moniker: member.Moniker})
		if err != nil {
			return nil, fmt.Errorf("failed to build validator record for %q: %w", member.OperatorAddress, err)
		}
		validator.Status = stakingtypes.Bonded
		if err := k.stakingKeeper.SetValidator(ctx, validator); err != nil {
			return nil, fmt.Errorf("failed to set validator record for %q: %w", member.OperatorAddress, err)
		}
		if err := k.stakingKeeper.SetValidatorByConsAddr(ctx, validator); err != nil {
			return nil, fmt.Errorf("failed to index validator record by consensus address for %q: %w", member.OperatorAddress, err)
		}
		valAddrBytes, err := sdk.ValAddressFromBech32(valAddrStr)
		if err != nil {
			return nil, fmt.Errorf("invalid valoper address %q: %w", valAddrStr, err)
		}
		consAddr, err := validator.GetConsAddr()
		if err != nil {
			return nil, fmt.Errorf("failed to derive consensus address for %q: %w", member.OperatorAddress, err)
		}
		if err := k.stakingKeeper.Hooks().AfterValidatorCreated(ctx, valAddrBytes); err != nil {
			return nil, fmt.Errorf("failed to run AfterValidatorCreated for %q: %w", member.OperatorAddress, err)
		}
		// AfterValidatorBonded is what actually creates x/slashing's ValidatorSigningInfo record
		// (via its own hook implementation) - without it, x/slashing's BeginBlocker panics the
		// whole chain the first time this member signs a block ("no validator signing info
		// found"), since GetValidatorSigningInfo has no fallback default (see docs/CHAIN.md).
		// Setting Status directly to Bonded above (rather than going through a real bonding
		// transition) never fires this hook on its own, so InitGenesis must call it explicitly.
		if err := k.stakingKeeper.Hooks().AfterValidatorBonded(ctx, consAddr, valAddrBytes); err != nil {
			return nil, fmt.Errorf("failed to run AfterValidatorBonded for %q: %w", member.OperatorAddress, err)
		}

		updates = append(updates, abci.ValidatorUpdate{PubKey: pkProto, Power: cometPower})
		if err := k.LastPower.Set(ctx, valAddrStr, cometPower); err != nil {
			return nil, fmt.Errorf("failed to record initial power for %q: %w", member.OperatorAddress, err)
		}
		if err := k.LastPubKey.Set(ctx, valAddrStr, member.ConsensusPubkey); err != nil {
			return nil, fmt.Errorf("failed to record initial pubkey for %q: %w", member.OperatorAddress, err)
		}
	}

	return updates, nil
}

// validatorUpdatesFromPowerRecords rebuilds the genesis CometBFT validator set from an exported
// genesis's power_records (security review B6: "return validator updates from the imported
// PowerRecords"). Every record in this list was, by construction, exported with strictly positive
// CometPower (Keeper.LastPower only ever stores positive powers - see recordAndBuildUpdate), so
// every one becomes a validator update here.
func validatorUpdatesFromPowerRecords(records []types.ValidatorPowerRecord) ([]abci.ValidatorUpdate, error) {
	updates := make([]abci.ValidatorUpdate, 0, len(records))
	for _, r := range records {
		if len(r.ConsensusPubkey) == 0 {
			return nil, fmt.Errorf("power record for %q has no consensus_pubkey, cannot rebuild its genesis validator update", r.OperatorAddress)
		}
		pkProto, err := pubKeyProto(r.ConsensusPubkey)
		if err != nil {
			return nil, fmt.Errorf("failed to encode consensus pubkey for %q: %w", r.OperatorAddress, err)
		}
		updates = append(updates, abci.ValidatorUpdate{PubKey: pkProto, Power: r.CometPower})
	}
	return updates, nil
}

// checkCommitteeSizeChainIDGate mirrors x/emission's checkBootstrapChainID: a production chain-id
// (one containing none of nonProductionChainIDMarkers) must declare at least
// types.ProductionMinCommitteeSize bootstrap committee members; a devnet/stagenet/localnet
// chain-id may declare as few as params.MinCommitteeSize (checked separately by
// GenesisState.Validate).
func checkCommitteeSizeChainIDGate(ctx sdk.Context, p types.Params, committeeSize int) error {
	if isNonProductionChainID(ctx.ChainID()) {
		return nil
	}
	if uint64(committeeSize) < types.ProductionMinCommitteeSize {
		return fmt.Errorf(
			"chain-id %q looks like a production chain-id (contains none of %v) but only declares %d bootstrap committee members, want at least %d",
			ctx.ChainID(), nonProductionChainIDMarkers, committeeSize, types.ProductionMinCommitteeSize,
		)
	}
	return nil
}

// valoperFromAccountBech32 converts a bech32 account address (BootstrapMember.OperatorAddress -
// "orama1...") to its bech32 validator-operator form ("oramavaloper1..."), the form x/staking uses
// for stakingtypes.Validator.OperatorAddress and the form x/power uses internally as the key for
// every per-validator collection (LastPower, LastPubKey, RampActivation, CommitteeSelfBond,
// BootstrapCommittee), so a committee member's entries always agree with what x/staking reports
// for them once they self-bond a real validator. Both bech32 strings encode the exact same
// underlying address bytes; only the human-readable prefix differs.
func valoperFromAccountBech32(accBech32 string) (string, error) {
	accAddr, err := sdk.AccAddressFromBech32(accBech32)
	if err != nil {
		return "", err
	}
	return sdk.ValAddress(accAddr).String(), nil
}

func isNonProductionChainID(chainID string) bool {
	for _, marker := range nonProductionChainIDMarkers {
		if strings.Contains(chainID, marker) {
			return true
		}
	}
	return false
}

// ExportGenesis reads x/power's current state back into a GenesisState, with Exported set so a
// later InitGenesis given this state takes the "continuing an existing chain" path (see
// InitGenesis's doc comment).
func (k Keeper) ExportGenesis(ctx sdk.Context) (*types.GenesisState, error) {
	p, err := k.Params.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get power params: %w", err)
	}
	lambda, err := k.Lambda.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get power lambda: %w", err)
	}
	lambdaEpoch, err := k.LambdaLastUpdatedEpoch.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get power lambda_last_updated_epoch: %w", err)
	}
	capBps, err := k.CapCurrentBps.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get power current_cap_bps: %w", err)
	}
	capStreak, err := k.CapBelowStreak.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get power below_step_up_streak_epochs: %w", err)
	}
	genesisEpoch, err := k.GenesisEpoch.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get power genesis_epoch: %w", err)
	}
	gateSatisfied, err := k.GateSatisfied.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get power gate_satisfied: %w", err)
	}

	var committee []types.BootstrapMember
	if err := k.BootstrapCommittee.Walk(ctx, nil, func(_ string, m types.BootstrapMember) (bool, error) {
		committee = append(committee, m)
		return false, nil
	}); err != nil {
		return nil, fmt.Errorf("failed to walk bootstrap committee: %w", err)
	}

	rampRecords, err := k.exportRampRecords(ctx)
	if err != nil {
		return nil, err
	}

	var powerRecords []types.ValidatorPowerRecord
	if err := k.LastPower.Walk(ctx, nil, func(addr string, power int64) (bool, error) {
		pubKey, err := k.LastPubKey.Get(ctx, addr)
		if err != nil && !isNotFound(err) {
			return false, fmt.Errorf("failed to load pubkey for power record %q: %w", addr, err)
		}
		powerRecords = append(powerRecords, types.ValidatorPowerRecord{OperatorAddress: addr, CometPower: power, ConsensusPubkey: pubKey})
		return false, nil
	}); err != nil {
		return nil, fmt.Errorf("failed to walk power records: %w", err)
	}

	var selfBonds []types.CommitteeSelfBondRecord
	if err := k.CommitteeSelfBond.Walk(ctx, nil, func(addr string, amt math.Int) (bool, error) {
		selfBonds = append(selfBonds, types.CommitteeSelfBondRecord{OperatorAddress: addr, ForceBonded: amt})
		return false, nil
	}); err != nil {
		return nil, fmt.Errorf("failed to walk committee self-bonds: %w", err)
	}

	return &types.GenesisState{
		Params:                  p,
		BootstrapCommittee:      committee,
		Lambda:                  lambda,
		LambdaLastUpdatedEpoch:  lambdaEpoch,
		CurrentCapBps:           capBps,
		BelowStepUpStreakEpochs: capStreak,
		GenesisEpoch:            genesisEpoch,
		RampRecords:             rampRecords,
		PowerRecords:            powerRecords,
		CommitteeSelfBonds:      selfBonds,
		Exported:                true,
		GateSatisfied:           gateSatisfied,
	}, nil
}
