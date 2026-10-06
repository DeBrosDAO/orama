package types

import (
	"fmt"
	"time"

	"cosmossdk.io/math"

	"github.com/DeBrosOfficial/network/chain/app/params"
)

// DefaultEpochDuration is the genesis default for Params.EpochDurationSeconds: 24 hours of BFT
// time, matching plans/open-network/track-c-chain.md C3.
const DefaultEpochDuration = 24 * time.Hour

// DefaultMinBlocksPerEpoch is the genesis default for Params.MinBlocksPerEpoch: MinBlocksFloor,
// the production minimum (see Validate).
const DefaultMinBlocksPerEpoch uint64 = MinBlocksFloor

// Absolute bounds on both genesis parameters, checked regardless of AllowBootstrapStake.
const (
	// MaxEpochDuration is the largest epoch_duration_seconds Validate accepts: 365 days.
	MaxEpochDuration = 365 * 24 * time.Hour
	// MaxBlocksPerEpoch is the largest min_blocks_per_epoch Validate accepts.
	MaxBlocksPerEpoch uint64 = 1_000_000_000
)

// Production floors, required unless AllowBootstrapStake is set (see Validate). Both must hold
// together so neither the clock nor the block count alone can force an epoch shut: at least
// MinBlocksFloor blocks must still be produced even if BFT time alone would already satisfy
// EpochDurationFloor, which is what makes the block count (not the timestamp a byzantine
// supermajority could otherwise manipulate) the binding brake - see docs/CHAIN.md.
const (
	// EpochDurationFloor is the minimum epoch_duration_seconds once AllowBootstrapStake is false:
	// 24 hours.
	EpochDurationFloor = 24 * time.Hour
	// MinBlocksFloor is the minimum min_blocks_per_epoch once AllowBootstrapStake is false.
	MinBlocksFloor uint64 = 14_400
)

// Faucet defaults (Params.Faucet*). The faucet itself is off by default.
const (
	// DefaultFaucetMaxDripOrama is the default faucet_max_drip, in ORAMA.
	DefaultFaucetMaxDripOrama = 1_000
	// DefaultFaucetEpochCapDrips is the default faucet_epoch_cap, in multiples of faucet_max_drip.
	DefaultFaucetEpochCapDrips = 100
	// DefaultFaucetRecipientCooldown is the default faucet_recipient_cooldown_seconds: 24 hours.
	DefaultFaucetRecipientCooldown uint64 = 86_400
)

// DefaultFaucetMaxDrip is the default faucet_max_drip in norama.
func DefaultFaucetMaxDrip() math.Int {
	return math.NewInt(DefaultFaucetMaxDripOrama).MulRaw(params.NoramaPerOrama)
}

// NewParams builds a Params from a Go duration and a minimum block count. allowBootstrapStake
// should be false for every production genesis; see Params.AllowBootstrapStake.
func NewParams(epochDuration time.Duration, minBlocksPerEpoch uint64, allowBootstrapStake bool) Params {
	return Params{
		EpochDurationSeconds: int64(epochDuration.Seconds()),
		MinBlocksPerEpoch:    minBlocksPerEpoch,
		AllowBootstrapStake:  allowBootstrapStake,

		FaucetEnabled:                  false,
		FaucetMaxDrip:                  DefaultFaucetMaxDrip(),
		FaucetEpochCap:                 DefaultFaucetMaxDrip().MulRaw(DefaultFaucetEpochCapDrips),
		FaucetRecipientCooldownSeconds: DefaultFaucetRecipientCooldown,
	}
}

// DefaultParams returns x/emission's genesis-default Params: a 24h epoch duration, a minimum of
// 14,400 blocks per epoch, and AllowBootstrapStake=false (a production genesis). A devnet or
// localnet genesis must explicitly opt into a shorter epoch by also setting AllowBootstrapStake
// (see chain/scripts/localnet and docs/CHAIN.md); once genesis has run none of these three values
// can ever change again: no x/emission Msg writes them (plans/open-network.md D18).
func DefaultParams() Params {
	return NewParams(DefaultEpochDuration, DefaultMinBlocksPerEpoch, false)
}

// EpochDuration returns the minimum epoch duration as a Go time.Duration.
func (p Params) EpochDuration() time.Duration {
	return time.Duration(p.EpochDurationSeconds) * time.Second
}

// Validate checks both genesis parameters against their absolute bounds - epoch_duration_seconds
// in (0, MaxEpochDuration] and min_blocks_per_epoch in (0, MaxBlocksPerEpoch] - and, unless
// AllowBootstrapStake is set, against the production floors (EpochDurationFloor,
// MinBlocksFloor) too.
func (p Params) Validate() error {
	if p.EpochDurationSeconds <= 0 {
		return fmt.Errorf("epoch_duration_seconds must be positive, got %d", p.EpochDurationSeconds)
	}
	// Compared in seconds: multiplying into a time.Duration first can overflow and wrap past the bound.
	if p.EpochDurationSeconds > int64(MaxEpochDuration/time.Second) {
		return fmt.Errorf("epoch_duration_seconds must be at most %s, got %s", MaxEpochDuration, p.EpochDuration())
	}
	if p.MinBlocksPerEpoch == 0 {
		return fmt.Errorf("min_blocks_per_epoch must be positive, got %d", p.MinBlocksPerEpoch)
	}
	if p.MinBlocksPerEpoch > MaxBlocksPerEpoch {
		return fmt.Errorf("min_blocks_per_epoch must be at most %d, got %d", MaxBlocksPerEpoch, p.MinBlocksPerEpoch)
	}

	if !p.AllowBootstrapStake {
		if p.EpochDuration() < EpochDurationFloor {
			return fmt.Errorf(
				"epoch_duration_seconds must be at least %s unless allow_bootstrap_stake is set, got %s",
				EpochDurationFloor, p.EpochDuration(),
			)
		}
		if p.MinBlocksPerEpoch < MinBlocksFloor {
			return fmt.Errorf(
				"min_blocks_per_epoch must be at least %d unless allow_bootstrap_stake is set, got %d",
				MinBlocksFloor, p.MinBlocksPerEpoch,
			)
		}
	}

	return p.validateFaucet()
}

// validateFaucet checks the faucet parameters: the amounts are never negative, and an enabled
// faucet has a positive max drip and an epoch cap that fits at least one max drip. Whether the
// chain-id may enable the faucet at all is checked in Keeper.InitGenesis, since the chain-id is
// not part of Params.
func (p Params) validateFaucet() error {
	maxDrip, epochCap := intOrZero(p.FaucetMaxDrip), intOrZero(p.FaucetEpochCap)
	if maxDrip.IsNegative() {
		return fmt.Errorf("faucet_max_drip must not be negative, got %s", maxDrip)
	}
	if epochCap.IsNegative() {
		return fmt.Errorf("faucet_epoch_cap must not be negative, got %s", epochCap)
	}
	if !p.FaucetEnabled {
		return nil
	}
	if !maxDrip.IsPositive() {
		return fmt.Errorf("faucet_max_drip must be positive when faucet_enabled is set, got %s", maxDrip)
	}
	if epochCap.LT(maxDrip) {
		return fmt.Errorf("faucet_epoch_cap (%s) must be at least faucet_max_drip (%s) when faucet_enabled is set", epochCap, maxDrip)
	}
	return nil
}

func intOrZero(v math.Int) math.Int {
	if v.IsNil() {
		return math.ZeroInt()
	}
	return v
}
