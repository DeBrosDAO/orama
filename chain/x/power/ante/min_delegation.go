package ante

import (
	"context"
	"errors"
	"fmt"

	errorsmod "cosmossdk.io/errors"
	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/DeBrosOfficial/network/chain/x/power/keeper"
)

// MinDelegationStaking is the subset of x/staking MinDelegationDecorator reads.
type MinDelegationStaking interface {
	GetValidator(ctx context.Context, addr sdk.ValAddress) (stakingtypes.Validator, error)
	GetDelegation(ctx context.Context, delAddr sdk.AccAddress, valAddr sdk.ValAddress) (stakingtypes.Delegation, error)
}

// MinDelegationDecorator rejects a transaction whose staking messages would leave
// any delegation strictly between zero and Params.MinDelegationForRewards.
// Messages in one transaction are applied in order, including
// MsgCancelUnbondingDelegation. A withdrawal is a full exit only when it
// consumes the delegation's shares, matching x/staking's ValidateUnbondAmount.
type MinDelegationDecorator struct {
	stakingKeeper MinDelegationStaking
	powerKeeper   keeper.Keeper
}

// NewMinDelegationDecorator builds a MinDelegationDecorator.
func NewMinDelegationDecorator(stakingKeeper MinDelegationStaking, powerKeeper keeper.Keeper) MinDelegationDecorator {
	return MinDelegationDecorator{stakingKeeper: stakingKeeper, powerKeeper: powerKeeper}
}

type delKey struct{ del, val string }

type delSim struct {
	vals map[string]stakingtypes.Validator
	dels map[delKey]math.LegacyDec
}

func (d MinDelegationDecorator) AnteHandle(ctx sdk.Context, tx sdk.Tx, simulate bool, next sdk.AnteHandler) (sdk.Context, error) {
	p, err := d.powerKeeper.Params.Get(ctx)
	if err != nil {
		return ctx, fmt.Errorf("failed to load power params: %w", err)
	}
	if p.MinDelegationForRewards.IsNil() || !p.MinDelegationForRewards.IsPositive() {
		return next(ctx, tx, simulate)
	}
	sim := delSim{
		vals: map[string]stakingtypes.Validator{},
		dels: map[delKey]math.LegacyDec{},
	}
	for _, msg := range tx.GetMsgs() {
		if err := d.apply(ctx, sim, msg, p.MinDelegationForRewards); err != nil {
			return ctx, err
		}
	}
	return next(ctx, tx, simulate)
}

func (d MinDelegationDecorator) apply(ctx sdk.Context, sim delSim, msg sdk.Msg, min math.Int) error {
	switch m := msg.(type) {
	case *stakingtypes.MsgCreateValidator:
		val, err := canonicalValidator(m.ValidatorAddress)
		if err != nil {
			return err
		}
		del := sdk.AccAddress(sdk.MustValAddressFromBech32(val)).String()
		return d.addTokens(ctx, sim, del, val, m.Value.Amount, min, true)
	case *stakingtypes.MsgDelegate:
		return d.withPair(m.DelegatorAddress, m.ValidatorAddress, func(del, val string) error {
			return d.addTokens(ctx, sim, del, val, m.Amount.Amount, min, false)
		})
	case *stakingtypes.MsgCancelUnbondingDelegation:
		return d.withPair(m.DelegatorAddress, m.ValidatorAddress, func(del, val string) error {
			return d.addTokens(ctx, sim, del, val, m.Amount.Amount, min, false)
		})
	case *stakingtypes.MsgUndelegate:
		return d.withPair(m.DelegatorAddress, m.ValidatorAddress, func(del, val string) error {
			_, err := d.removeTokens(ctx, sim, del, val, m.Amount.Amount, min)
			return err
		})
	case *stakingtypes.MsgBeginRedelegate:
		del, src, err := canonicalDelegatorAndValidator(m.DelegatorAddress, m.ValidatorSrcAddress)
		if err != nil {
			return err
		}
		dst, err := canonicalValidator(m.ValidatorDstAddress)
		if err != nil {
			return err
		}
		removed, err := d.removeTokens(ctx, sim, del, src, m.Amount.Amount, min)
		if err != nil {
			return err
		}
		if !removed.IsPositive() {
			return errorsmod.Wrap(sdkerrors.ErrInvalidRequest, "redelegation removes no tokens")
		}
		return d.addTokens(ctx, sim, del, dst, removed, min, false)
	default:
		return nil
	}
}

func (d MinDelegationDecorator) withPair(del, val string, fn func(del, val string) error) error {
	del, val, err := canonicalDelegatorAndValidator(del, val)
	if err != nil {
		return err
	}
	return fn(del, val)
}

func canonicalDelegatorAndValidator(del, val string) (string, string, error) {
	del, err := canonicalAccount(del)
	if err != nil {
		return "", "", err
	}
	val, err = canonicalValidator(val)
	if err != nil {
		return "", "", err
	}
	return del, val, nil
}

func (d MinDelegationDecorator) addTokens(ctx sdk.Context, sim delSim, del, val string, amount, min math.Int, create bool) error {
	if amount.IsNil() || !amount.IsPositive() {
		return errorsmod.Wrapf(sdkerrors.ErrInvalidRequest, "delegation amount must be positive, got %s", amount)
	}
	validator, delShares, err := d.load(ctx, sim, del, val, create)
	if err != nil {
		return err
	}
	updated, issued := validator.AddTokensFromDel(amount)
	delShares = delShares.Add(issued)
	if err := requireTokens(updated, delShares, min, "resulting delegation"); err != nil {
		return err
	}
	sim.vals[val] = updated
	sim.dels[delKey{del, val}] = delShares
	return nil
}

func (d MinDelegationDecorator) removeTokens(ctx sdk.Context, sim delSim, del, val string, amount, min math.Int) (math.Int, error) {
	if amount.IsNil() || !amount.IsPositive() {
		return math.Int{}, errorsmod.Wrapf(sdkerrors.ErrInvalidRequest, "withdrawal amount must be positive, got %s", amount)
	}
	validator, delShares, err := d.load(ctx, sim, del, val, false)
	if err != nil {
		return math.Int{}, err
	}
	if delShares.IsZero() || validator.DelegatorShares.IsZero() || validator.Tokens.IsZero() {
		return math.Int{}, errorsmod.Wrap(sdkerrors.ErrInvalidRequest, "no delegation to withdraw")
	}
	shares, err := validator.SharesFromTokens(amount)
	if err != nil {
		return math.Int{}, errorsmod.Wrap(sdkerrors.ErrInvalidRequest, err.Error())
	}
	truncated, err := validator.SharesFromTokensTruncated(amount)
	if err != nil {
		return math.Int{}, errorsmod.Wrap(sdkerrors.ErrInvalidRequest, err.Error())
	}
	if truncated.GT(delShares) {
		return math.Int{}, errorsmod.Wrapf(sdkerrors.ErrInvalidRequest, "cannot withdraw %s from a delegation of %s shares", amount, delShares)
	}
	if shares.GT(delShares) {
		shares = delShares
	}
	remaining := delShares.Sub(shares)
	if remaining.IsPositive() {
		if err := requireTokens(validator, remaining, min, "delegation left after withdrawal"); err != nil {
			return math.Int{}, err
		}
	}
	updated, removed := validator.RemoveDelShares(shares)
	sim.vals[val] = updated
	sim.dels[delKey{del, val}] = remaining
	return removed, nil
}

func (d MinDelegationDecorator) load(ctx sdk.Context, sim delSim, del, val string, create bool) (stakingtypes.Validator, math.LegacyDec, error) {
	key := delKey{del, val}
	if validator, ok := sim.vals[val]; ok {
		if shares, seen := sim.dels[key]; seen {
			return validator, shares, nil
		}
		shares, err := d.delegationShares(ctx, del, val)
		if err != nil {
			return stakingtypes.Validator{}, math.LegacyDec{}, err
		}
		sim.dels[key] = shares
		return validator, shares, nil
	}
	valAddr, err := sdk.ValAddressFromBech32(val)
	if err != nil {
		return stakingtypes.Validator{}, math.LegacyDec{}, errorsmod.Wrap(sdkerrors.ErrInvalidAddress, err.Error())
	}
	validator, err := d.stakingKeeper.GetValidator(ctx, valAddr)
	if err != nil {
		if errors.Is(err, stakingtypes.ErrNoValidatorFound) && create {
			validator = stakingtypes.Validator{OperatorAddress: val, Tokens: math.ZeroInt(), DelegatorShares: math.LegacyZeroDec()}
		} else if errors.Is(err, stakingtypes.ErrNoValidatorFound) {
			return stakingtypes.Validator{}, math.LegacyDec{}, errorsmod.Wrapf(sdkerrors.ErrInvalidRequest, "validator %s does not exist", val)
		} else if err != nil {
			return stakingtypes.Validator{}, math.LegacyDec{}, fmt.Errorf("failed to load validator %q: %w", val, err)
		}
	}
	shares, err := d.delegationShares(ctx, del, val)
	if err != nil {
		return stakingtypes.Validator{}, math.LegacyDec{}, err
	}
	sim.vals[val] = validator
	sim.dels[key] = shares
	return validator, shares, nil
}

func (d MinDelegationDecorator) delegationShares(ctx sdk.Context, del, val string) (math.LegacyDec, error) {
	delAddr, err := sdk.AccAddressFromBech32(del)
	if err != nil {
		return math.LegacyDec{}, errorsmod.Wrap(sdkerrors.ErrInvalidAddress, err.Error())
	}
	valAddr, err := sdk.ValAddressFromBech32(val)
	if err != nil {
		return math.LegacyDec{}, errorsmod.Wrap(sdkerrors.ErrInvalidAddress, err.Error())
	}
	delegation, err := d.stakingKeeper.GetDelegation(ctx, delAddr, valAddr)
	if err == nil {
		return delegation.Shares, nil
	}
	if errors.Is(err, stakingtypes.ErrNoDelegation) {
		return math.LegacyZeroDec(), nil
	}
	return math.LegacyDec{}, fmt.Errorf("failed to load delegation %s -> %s: %w", del, val, err)
}

func requireTokens(validator stakingtypes.Validator, shares math.LegacyDec, min math.Int, what string) error {
	var tokens math.Int
	if validator.DelegatorShares.IsZero() {
		tokens = math.ZeroInt()
	} else {
		tokens = validator.TokensFromSharesTruncated(shares).TruncateInt()
	}
	if tokens.LT(min) {
		return errorsmod.Wrapf(sdkerrors.ErrInvalidRequest, "%s must be at least %s, got %s", what, min, tokens)
	}
	return nil
}
