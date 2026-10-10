package app

import (
	"encoding/json"
	"fmt"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"

	servertypes "github.com/cosmos/cosmos-sdk/server/types"
	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	slashingtypes "github.com/cosmos/cosmos-sdk/x/slashing/types"
	"github.com/cosmos/cosmos-sdk/x/staking"
	stakingkeeper "github.com/cosmos/cosmos-sdk/x/staking/keeper"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
)

// ExportAppStateAndValidators exports the application's state for a new genesis file, e.g. ahead
// of a coordinated hard fork (plans/open-network.md D20).
func (app *OramaApp) ExportAppStateAndValidators(forZeroHeight bool, jailAllowedAddrs, modulesToExport []string) (servertypes.ExportedApp, error) {
	ctx := app.NewContextLegacy(true, cmtproto.Header{Height: app.LastBlockHeight()})

	// CometBFT starts InitChain at last height + 1.
	height := app.LastBlockHeight() + 1
	if forZeroHeight {
		height = 0
		if err := app.prepForZeroHeightGenesis(ctx, jailAllowedAddrs); err != nil {
			return servertypes.ExportedApp{}, fmt.Errorf("failed to prepare zero-height genesis: %w", err)
		}
	}

	genState, err := app.ModuleManager.ExportGenesisForModules(ctx, app.appCodec, modulesToExport)
	if err != nil {
		return servertypes.ExportedApp{}, fmt.Errorf("failed to export genesis for modules: %w", err)
	}

	appState, err := json.MarshalIndent(genState, "", "  ")
	if err != nil {
		return servertypes.ExportedApp{}, fmt.Errorf("failed to marshal exported app state: %w", err)
	}

	validators, err := staking.WriteValidators(ctx, app.StakingKeeper)
	if err != nil {
		return servertypes.ExportedApp{}, fmt.Errorf("failed to write validators: %w", err)
	}
	return servertypes.ExportedApp{
		AppState:        appState,
		Validators:      validators,
		Height:          height,
		ConsensusParams: app.GetConsensusParams(ctx),
	}, nil
}

// iterateValidators runs IterateValidators, capturing an error the callback reports through the
// returned bool, so a failure inside the callback (which itself cannot return an error) actually
// surfaces to the caller instead of just stopping iteration silently.
func iterateValidators(ctx sdk.Context, k *stakingkeeper.Keeper, fn func(val stakingtypes.ValidatorI) error) error {
	var callbackErr error
	if err := k.IterateValidators(ctx, func(_ int64, val stakingtypes.ValidatorI) bool {
		if err := fn(val); err != nil {
			callbackErr = err
			return true
		}
		return false
	}); err != nil {
		return fmt.Errorf("failed to iterate validators: %w", err)
	}
	return callbackErr
}

// prepForZeroHeightGenesis resets state that only makes sense relative to a specific height, so
// the exported genesis can restart a fresh chain at height zero. Every failure is returned to the
// caller rather than panicking or exiting the process, so ExportAppStateAndValidators (and, in
// turn, `oramad export`) can report it as an ordinary error.
func (app *OramaApp) prepForZeroHeightGenesis(ctx sdk.Context, jailAllowedAddrs []string) error {
	applyAllowedAddrs := len(jailAllowedAddrs) > 0

	allowedAddrsMap := make(map[string]bool)
	for _, addr := range jailAllowedAddrs {
		if _, err := sdk.ValAddressFromBech32(addr); err != nil {
			return fmt.Errorf("invalid jail-allowed address %q: %w", addr, err)
		}
		allowedAddrsMap[addr] = true
	}

	// Withdraw all validator commission and delegator rewards before resetting heights, so
	// nothing is lost to the reset.
	if err := iterateValidators(ctx, app.StakingKeeper, func(val stakingtypes.ValidatorI) error {
		valBz, err := app.StakingKeeper.ValidatorAddressCodec().StringToBytes(val.GetOperator())
		if err != nil {
			return fmt.Errorf("failed to decode validator operator address %q: %w", val.GetOperator(), err)
		}
		if _, err := app.DistrKeeper.WithdrawValidatorCommission(ctx, valBz); err != nil {
			return fmt.Errorf("failed to withdraw commission for validator %s: %w", val.GetOperator(), err)
		}
		return nil
	}); err != nil {
		return err
	}

	dels, err := app.StakingKeeper.GetAllDelegations(ctx)
	if err != nil {
		return fmt.Errorf("failed to list delegations: %w", err)
	}
	for _, delegation := range dels {
		valAddr, err := sdk.ValAddressFromBech32(delegation.ValidatorAddress)
		if err != nil {
			return fmt.Errorf("invalid validator address %q in delegation: %w", delegation.ValidatorAddress, err)
		}
		delAddr := sdk.MustAccAddressFromBech32(delegation.DelegatorAddress)
		if _, err := app.DistrKeeper.WithdrawDelegationRewards(ctx, delAddr, valAddr); err != nil {
			return fmt.Errorf("failed to withdraw delegation rewards for %s -> %s: %w", delegation.DelegatorAddress, delegation.ValidatorAddress, err)
		}
	}

	app.DistrKeeper.DeleteAllValidatorSlashEvents(ctx)
	app.DistrKeeper.DeleteAllValidatorHistoricalRewards(ctx)

	height := ctx.BlockHeight()
	zeroHeightCtx := ctx.WithBlockHeight(0)

	if err := iterateValidators(zeroHeightCtx, app.StakingKeeper, func(val stakingtypes.ValidatorI) error {
		valBz, err := app.StakingKeeper.ValidatorAddressCodec().StringToBytes(val.GetOperator())
		if err != nil {
			return fmt.Errorf("failed to decode validator operator address %q: %w", val.GetOperator(), err)
		}
		scraps, err := app.DistrKeeper.GetValidatorOutstandingRewardsCoins(zeroHeightCtx, valBz)
		if err != nil {
			return fmt.Errorf("failed to get outstanding rewards for validator %s: %w", val.GetOperator(), err)
		}
		feePool, err := app.DistrKeeper.FeePool.Get(zeroHeightCtx)
		if err != nil {
			return fmt.Errorf("failed to get the fee pool: %w", err)
		}
		feePool.CommunityPool = feePool.CommunityPool.Add(scraps...)
		if err := app.DistrKeeper.FeePool.Set(zeroHeightCtx, feePool); err != nil {
			return fmt.Errorf("failed to set the fee pool: %w", err)
		}
		if err := app.DistrKeeper.Hooks().AfterValidatorCreated(zeroHeightCtx, valBz); err != nil {
			return fmt.Errorf("failed to reinitialize validator %s: %w", val.GetOperator(), err)
		}
		return nil
	}); err != nil {
		return err
	}

	for _, del := range dels {
		valAddr, err := sdk.ValAddressFromBech32(del.ValidatorAddress)
		if err != nil {
			return fmt.Errorf("invalid validator address %q in delegation: %w", del.ValidatorAddress, err)
		}
		delAddr := sdk.MustAccAddressFromBech32(del.DelegatorAddress)

		if err := app.DistrKeeper.Hooks().BeforeDelegationCreated(zeroHeightCtx, delAddr, valAddr); err != nil {
			return fmt.Errorf("failed to increment delegation period for %s -> %s: %w", del.DelegatorAddress, del.ValidatorAddress, err)
		}
		if err := app.DistrKeeper.Hooks().AfterDelegationModified(zeroHeightCtx, delAddr, valAddr); err != nil {
			return fmt.Errorf("failed to create a new delegation period record for %s -> %s: %w", del.DelegatorAddress, del.ValidatorAddress, err)
		}
	}

	ctx = ctx.WithBlockHeight(height)

	var redelegationErr error
	if err := app.StakingKeeper.IterateRedelegations(ctx, func(_ int64, red stakingtypes.Redelegation) bool {
		for i := range red.Entries {
			red.Entries[i].CreationHeight = 0
		}
		if err := app.StakingKeeper.SetRedelegation(ctx, red); err != nil {
			redelegationErr = fmt.Errorf("failed to reset redelegation creation height for %s -> %s: %w", red.DelegatorAddress, red.ValidatorDstAddress, err)
			return true
		}
		return false
	}); err != nil {
		return fmt.Errorf("failed to iterate redelegations: %w", err)
	}
	if redelegationErr != nil {
		return redelegationErr
	}

	var unbondingErr error
	if err := app.StakingKeeper.IterateUnbondingDelegations(ctx, func(_ int64, ubd stakingtypes.UnbondingDelegation) bool {
		for i := range ubd.Entries {
			ubd.Entries[i].CreationHeight = 0
		}
		if err := app.StakingKeeper.SetUnbondingDelegation(ctx, ubd); err != nil {
			unbondingErr = fmt.Errorf("failed to reset unbonding delegation creation height for %s: %w", ubd.DelegatorAddress, err)
			return true
		}
		return false
	}); err != nil {
		return fmt.Errorf("failed to iterate unbonding delegations: %w", err)
	}
	if unbondingErr != nil {
		return unbondingErr
	}

	store := ctx.KVStore(app.GetKey(stakingtypes.StoreKey))
	iter := storetypes.KVStoreReversePrefixIterator(store, stakingtypes.ValidatorsKey)
	for ; iter.Valid(); iter.Next() {
		addr := sdk.ValAddress(stakingtypes.AddressFromValidatorsKey(iter.Key()))
		validator, err := app.StakingKeeper.GetValidator(ctx, addr)
		if err != nil {
			_ = iter.Close()
			return fmt.Errorf("expected validator %s, not found: %w", addr, err)
		}

		validator.UnbondingHeight = 0
		if applyAllowedAddrs && !allowedAddrsMap[addr.String()] {
			validator.Jailed = true
		}

		if err := app.StakingKeeper.SetValidator(ctx, validator); err != nil {
			_ = iter.Close()
			return fmt.Errorf("failed to set validator %s: %w", addr, err)
		}
	}
	if err := iter.Close(); err != nil {
		return fmt.Errorf("failed to close the staking validators iterator: %w", err)
	}

	if _, err := app.StakingKeeper.ApplyAndReturnValidatorSetUpdates(ctx); err != nil {
		return fmt.Errorf("failed to apply validator set updates: %w", err)
	}

	var signingInfoErr error
	if err := app.SlashingKeeper.IterateValidatorSigningInfos(
		ctx,
		func(addr sdk.ConsAddress, info slashingtypes.ValidatorSigningInfo) bool {
			info.StartHeight = 0
			if err := app.SlashingKeeper.SetValidatorSigningInfo(ctx, addr, info); err != nil {
				signingInfoErr = fmt.Errorf("failed to reset signing info start height for %s: %w", addr, err)
				return true
			}
			return false
		},
	); err != nil {
		return fmt.Errorf("failed to iterate validator signing infos: %w", err)
	}
	if signingInfoErr != nil {
		return signingInfoErr
	}

	return nil
}
