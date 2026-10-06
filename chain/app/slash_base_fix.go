package app

import (
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingkeeper "github.com/cosmos/cosmos-sdk/x/staking/keeper"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
)

// slashBaseFix wraps x/staking's keeper as the StakingKeeper dependency this app gives to
// x/slashing (which x/evidence, in turn, routes every slash call through - see
// x/evidence/types.SlashingKeeper), fixing the base amount a slash is computed from
// (security review B3).
//
// Stock x/staking's own Slash/SlashWithInfractionReason take a "power" argument and slash
// TokensFromConsensusPower(power) * fraction of the validator's stake - power meaning literal
// CometBFT consensus power, on the assumption that power is proportional to real bonded tokens at
// PowerReduction (1e6) norama per unit (sdk.DefaultPowerReduction, which x/staking's own
// PowerReduction always returns - it is not configurable in this SDK version). x/power's own
// CometBFT power is emphatically NOT proportional to real stake at that ratio: it is capped,
// redistributed, ramped and blended with the bootstrap committee's equal share, and scaled by
// Params.CometPowerScale (1e9 by default) - a completely different fixed point. Evidence and
// slashing source their "power" argument straight from CometBFT's own reporting (a vote's power in
// LastCommitInfo, or an equivocation's GetValidatorPower()), so passing it straight into
// TokensFromConsensusPower would slash 1e9/1e6 = 1000x too much on that scale mismatch alone,
// before even accounting for capping - the review measured 83-100% of a validator's real stake
// burned by a single infraction, when the spec calls for exactly 5% (double-sign) or 0.01%
// (downtime).
//
// The fix: ignore the given power entirely and substitute the EQUIVALENT power the validator's own
// CURRENT real bonded tokens represent at the stock PowerReduction ratio
// (tokens / PowerReduction), then delegate to the real underlying keeper. TokensFromConsensusPower
// then recovers (up to PowerReduction-1 norama of integer-division rounding, at most
// 999,999 norama = 0.000999999 ORAMA - negligible) the validator's real tokens, so the requested
// fraction is slashed from what it actually has, not from a number derived from a mismatched power
// scale.
type slashBaseFix struct {
	*stakingkeeper.Keeper
}

// newSlashBaseFix wraps keeper.
func newSlashBaseFix(keeper *stakingkeeper.Keeper) slashBaseFix {
	return slashBaseFix{Keeper: keeper}
}

// Slash overrides the embedded keeper's Slash to fix the base amount (see the type doc comment).
func (s slashBaseFix) Slash(ctx context.Context, consAddr sdk.ConsAddress, infractionHeight, _ int64, slashFactor math.LegacyDec) (math.Int, error) {
	return s.slashOnRealTokens(ctx, consAddr, infractionHeight, slashFactor, nil)
}

// SlashWithInfractionReason overrides the embedded keeper's SlashWithInfractionReason to fix the
// base amount (see the type doc comment).
func (s slashBaseFix) SlashWithInfractionReason(ctx context.Context, consAddr sdk.ConsAddress, infractionHeight, _ int64, slashFactor math.LegacyDec, infraction stakingtypes.Infraction) (math.Int, error) {
	return s.slashOnRealTokens(ctx, consAddr, infractionHeight, slashFactor, &infraction)
}

func (s slashBaseFix) slashOnRealTokens(ctx context.Context, consAddr sdk.ConsAddress, infractionHeight int64, slashFactor math.LegacyDec, infraction *stakingtypes.Infraction) (math.Int, error) {
	validator, err := s.Keeper.GetValidatorByConsAddr(ctx, consAddr)
	if err != nil {
		if errors.Is(err, stakingtypes.ErrNoValidatorFound) {
			// Matches stock Slash's own handling of this case: the validator must have already
			// been fully removed, so there is nothing left to slash.
			return math.ZeroInt(), nil
		}
		return math.ZeroInt(), fmt.Errorf("slashBaseFix: failed to load validator by consensus address: %w", err)
	}

	// Stock Slash treats `power` as the whole slash budget, then subtracts the
	// slashes it applies to unbonding and redelegation entries before burning
	// the rest from current bonded tokens. The base therefore has to include
	// the stake that left after the infraction, or the tokens still bonded are
	// slashed by less than slashFactor.
	departed, err := s.slashableDepartedTokens(ctx, validator, infractionHeight)
	if err != nil {
		return math.ZeroInt(), err
	}
	powerReduction := s.Keeper.PowerReduction(ctx)
	equivalentPower := validator.Tokens.Add(departed).Quo(powerReduction).Int64()

	if infraction != nil {
		return s.Keeper.SlashWithInfractionReason(ctx, consAddr, infractionHeight, equivalentPower, slashFactor, *infraction)
	}
	return s.Keeper.Slash(ctx, consAddr, infractionHeight, equivalentPower, slashFactor)
}
