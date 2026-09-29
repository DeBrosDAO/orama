// Package ante implements x/fees's replacement for x/auth/ante's stock DeductFeeDecorator
// (plans/open-network/track-c-chain.md C2): an EIP-1559-style base fee, burned in full, with the
// tip going to the current block's proposer, and a fee payer's own earnings account used as a
// fallback (base fee only - see keeper.Keeper.SettleFee) when their bank balance is short.
package ante

import (
	"bytes"
	"context"
	"fmt"
	stdmath "math"

	errorsmod "cosmossdk.io/errors"
	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/x/fees/keeper"
)

// AccountKeeper is the subset of x/auth's keeper this decorator needs: checking that the account
// paying a fee actually exists, matching x/auth/ante's own DeductFeeDecorator behavior.
type AccountKeeper interface {
	GetAccount(ctx context.Context, addr sdk.AccAddress) sdk.AccountI
}

// FeegrantKeeper is the subset of x/feegrant's keeper this decorator needs; it has the same shape
// as x/auth/ante.FeegrantKeeper so a fee granter can still sponsor a signer's fees.
type FeegrantKeeper interface {
	UseGrantedFees(ctx context.Context, granter, grantee sdk.AccAddress, fee sdk.Coins, msgs []sdk.Msg) error
}

// StakingKeeper is the subset of x/staking's keeper this decorator needs to resolve the current
// block's proposer (from its consensus address, in the block header) to the operator account that
// owns its earnings.
type StakingKeeper interface {
	GetValidatorByConsAddr(ctx context.Context, consAddr sdk.ConsAddress) (stakingtypes.Validator, error)
}

// FeeDecorator replaces x/auth/ante's NewDeductFeeDecorator in this app's ante chain (see
// app.setAnteHandler). It requires every tx to declare a fee (in norama) of at least the current
// base fee times its gas limit; anything above that is treated as a tip to the proposer.
type FeeDecorator struct {
	accountKeeper  AccountKeeper
	feegrantKeeper FeegrantKeeper
	stakingKeeper  StakingKeeper
	feesKeeper     keeper.Keeper
}

// NewFeeDecorator builds a FeeDecorator. feegrantKeeper may be nil (no fee sponsorship).
func NewFeeDecorator(ak AccountKeeper, fk FeegrantKeeper, sk StakingKeeper, feesKeeper keeper.Keeper) FeeDecorator {
	return FeeDecorator{accountKeeper: ak, feegrantKeeper: fk, stakingKeeper: sk, feesKeeper: feesKeeper}
}

func (d FeeDecorator) AnteHandle(ctx sdk.Context, tx sdk.Tx, simulate bool, next sdk.AnteHandler) (sdk.Context, error) {
	feeTx, ok := tx.(sdk.FeeTx)
	if !ok {
		return ctx, errorsmod.Wrap(sdkerrors.ErrTxDecode, "tx must be a FeeTx")
	}
	if !simulate && ctx.BlockHeight() > 0 && feeTx.GetGas() == 0 {
		return ctx, errorsmod.Wrap(sdkerrors.ErrInvalidGasLimit, "must provide positive gas")
	}

	if simulate {
		// RootWallet review: a simulation's fee/gas are not final yet (a `--gas auto` estimate
		// typically simulates with an empty fee and gas 0), so neither the local mempool's
		// minimum-gas-prices policy below nor x/fees' own base-fee check further down may run here -
		// mirroring upstream x/auth/ante's own DeductFeeDecorator, which only calls its equivalent
		// txFeeChecker when !simulate. Security review, non-blocking "simulation": still run the
		// same fee-settlement work on a branched, discarded context so its gas consumption is
		// counted (making the `--gas auto` estimate accurate), without persisting anything or
		// failing the simulation over a not-yet-finalized fee/gas guess.
		d.simulateSettle(ctx, feeTx)
		return next(ctx, tx, simulate)
	}

	// Security review, non-blocking "min gas price": this is the per-validator LOCAL mempool
	// admission policy (app.toml's minimum-gas-prices), independent of and in addition to x/fees'
	// own chain-wide base fee below - see docs/CHAIN.md. It only ever runs on CheckTx (a node's own
	// mempool policy has no business affecting DeliverTx/FinalizeBlock's deterministic state
	// transition), and it also sets the tx's mempool priority.
	priority, err := checkValidatorMinGasPrice(ctx, feeTx)
	if err != nil {
		return ctx, err
	}
	ctx = ctx.WithPriority(priority)

	baseFee, err := d.feesKeeper.BaseFee.Get(ctx)
	if err != nil {
		return ctx, fmt.Errorf("failed to load the base fee: %w", err)
	}
	baseFeeAmount := baseFee.Mul(math.NewIntFromUint64(feeTx.GetGas()))

	fee := feeTx.GetFee().AmountOf(params.BaseDenom)
	if fee.LT(baseFeeAmount) {
		return ctx, errorsmod.Wrapf(
			sdkerrors.ErrInsufficientFee,
			"insufficient fee: got %s%s, want at least the base fee of %s%s (gas limit %d x base fee %s)",
			fee, params.BaseDenom, baseFeeAmount, params.BaseDenom, feeTx.GetGas(), baseFee,
		)
	}
	tipAmount := fee.Sub(baseFeeAmount)

	payer, allowEarningsForBase, err := d.resolvePayer(ctx, feeTx)
	if err != nil {
		return ctx, err
	}
	proposer, found, err := d.resolveProposer(ctx)
	if err != nil {
		return ctx, err
	}
	if !found {
		// Security review, non-blocking "unresolvable proposer": never credit an unattributed tip
		// to x/fees' own module address (an unintended accumulation point nobody can ever spend
		// from) - burn the whole fee, base and tip alike, instead. This should not happen in
		// normal operation; a malformed or missing block header field must never be able to block
		// every transaction on the chain, so this degrades to a (harmless, in-spec) full burn
		// rather than failing the tx.
		baseFeeAmount, tipAmount = baseFeeAmount.Add(tipAmount), math.ZeroInt()
		proposer = payer // SettleFee never actually pays proposer when tipAmount is zero.
	}

	if err := d.feesKeeper.SettleFee(ctx, payer, proposer, baseFeeAmount, tipAmount, allowEarningsForBase); err != nil {
		return ctx, errorsmod.Wrap(sdkerrors.ErrInsufficientFunds, err.Error())
	}

	ctx.EventManager().EmitEvent(sdk.NewEvent(
		sdk.EventTypeTx,
		sdk.NewAttribute(sdk.AttributeKeyFee, sdk.NewCoins(sdk.NewCoin(params.BaseDenom, fee)).String()),
		sdk.NewAttribute("base_fee", baseFeeAmount.String()),
		sdk.NewAttribute("tip", tipAmount.String()),
	))

	return next(ctx, tx, simulate)
}

// simulateSettle best-effort runs fee settlement on a branched, discarded context purely so its gas
// consumption is reflected in a simulation's gas estimate. Any error (insufficient funds, an
// unresolvable payer/proposer, a not-yet-existing account) is swallowed: the real fee and gas limit
// are not final yet during simulation, so this must never fail the simulation itself.
func (d FeeDecorator) simulateSettle(ctx sdk.Context, feeTx sdk.FeeTx) {
	cacheCtx, _ := ctx.CacheContext()

	baseFee, err := d.feesKeeper.BaseFee.Get(cacheCtx)
	if err != nil {
		return
	}
	baseFeeAmount := baseFee.Mul(math.NewIntFromUint64(feeTx.GetGas()))
	fee := feeTx.GetFee().AmountOf(params.BaseDenom)
	tipAmount := fee.Sub(baseFeeAmount)
	if tipAmount.IsNegative() {
		tipAmount = math.ZeroInt()
		baseFeeAmount = fee
	}

	payer, allowEarningsForBase, err := d.resolvePayer(cacheCtx, feeTx)
	if err != nil {
		return
	}
	proposer, found, err := d.resolveProposer(cacheCtx)
	if err != nil || !found {
		return
	}
	_ = d.feesKeeper.SettleFee(cacheCtx, payer, proposer, baseFeeAmount, tipAmount, allowEarningsForBase)
}

// checkValidatorMinGasPrice enforces this validator's own local mempool minimum-gas-prices policy
// on CheckTx only (mirroring x/auth/ante's own unexported checkTxFeeWithValidatorMinGasPrices), and
// returns the tx's mempool priority either way.
func checkValidatorMinGasPrice(ctx sdk.Context, feeTx sdk.FeeTx) (int64, error) {
	feeCoins := feeTx.GetFee()
	gas := feeTx.GetGas()

	if ctx.IsCheckTx() {
		minGasPrices := ctx.MinGasPrices()
		if !minGasPrices.IsZero() {
			requiredFees := make(sdk.Coins, len(minGasPrices))
			glDec := math.LegacyNewDec(int64(gas))
			for i, gp := range minGasPrices {
				fee := gp.Amount.Mul(glDec)
				requiredFees[i] = sdk.NewCoin(gp.Denom, fee.Ceil().RoundInt())
			}
			if !feeCoins.IsAnyGTE(requiredFees) {
				return 0, errorsmod.Wrapf(sdkerrors.ErrInsufficientFee, "insufficient fees; got: %s required: %s", feeCoins, requiredFees)
			}
		}
	}

	var priority int64
	for _, c := range feeCoins {
		p := int64(stdmath.MaxInt64)
		if gas > 0 {
			gasPrice := c.Amount.QuoRaw(int64(gas))
			if gasPrice.IsInt64() {
				p = gasPrice.Int64()
			}
		}
		if priority == 0 || p < priority {
			priority = p
		}
	}
	return priority, nil
}

// resolvePayer returns the account that owes this tx's fee: the fee granter (if one is set and
// authorizes it) or otherwise the first signer, matching x/auth/ante's own DeductFeeDecorator. The
// second return value is whether the base fee may fall back to the payer's earnings if their bank
// balance is short - true only when the signer is paying with their own funds, never when a fee
// granter is sponsoring (security review, non-blocking "fee granter").
func (d FeeDecorator) resolvePayer(ctx sdk.Context, feeTx sdk.FeeTx) (sdk.AccAddress, bool, error) {
	feePayer := sdk.AccAddress(feeTx.FeePayer())
	feeGranter := feeTx.FeeGranter()
	payer := feePayer
	allowEarningsForBase := true

	if feeGranter != nil {
		granterAddr := sdk.AccAddress(feeGranter)
		if !bytes.Equal(granterAddr, feePayer) {
			if d.feegrantKeeper == nil {
				return nil, false, errorsmod.Wrap(sdkerrors.ErrInvalidRequest, "fee grants are not enabled")
			}
			if err := d.feegrantKeeper.UseGrantedFees(ctx, granterAddr, feePayer, feeTx.GetFee(), feeTx.GetMsgs()); err != nil {
				return nil, false, errorsmod.Wrapf(err, "%s does not allow paying fees for %s", granterAddr, feePayer)
			}
			allowEarningsForBase = false
		}
		payer = granterAddr
	}

	if d.accountKeeper.GetAccount(ctx, payer) == nil {
		return nil, false, sdkerrors.ErrUnknownAddress.Wrapf("fee payer address %s does not exist", payer)
	}
	return payer, allowEarningsForBase, nil
}

// resolveProposer returns the account address that owns the current block's proposer validator,
// and whether one could be resolved at all - a malformed or missing block header field must never
// be able to block every transaction in the chain (see the caller's handling of found == false).
func (d FeeDecorator) resolveProposer(ctx sdk.Context) (sdk.AccAddress, bool, error) {
	addr, found := ResolveProposer(ctx, d.stakingKeeper)
	return addr, found, nil
}

// ResolveProposer returns the account that owns the current block's proposer validator, and
// whether one resolved. Other modules that pay the proposer a tip use it, so the rule is one.
func ResolveProposer(ctx sdk.Context, sk StakingKeeper) (sdk.AccAddress, bool) {
	consAddr := sdk.ConsAddress(ctx.BlockHeader().ProposerAddress)
	if len(consAddr) == 0 {
		return nil, false
	}
	validator, err := sk.GetValidatorByConsAddr(ctx, consAddr)
	if err != nil {
		return nil, false
	}
	valAddr, err := sdk.ValAddressFromBech32(validator.OperatorAddress)
	if err != nil {
		return nil, false
	}
	return sdk.AccAddress(valAddr), true
}
