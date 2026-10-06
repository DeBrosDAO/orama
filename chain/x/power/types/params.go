package types

import (
	"fmt"

	"cosmossdk.io/math"
)

// ProductionMinCommitteeSize is the minimum bootstrap committee size Keeper.InitGenesis requires
// on a production chain-id (plans/open-network.md D16: "at least 30"). A devnet, stagenet or
// localnet chain-id may declare as few as MinCommitteeSizeFloor instead (see
// keeper.checkCommitteeSizeChainIDGate).
const ProductionMinCommitteeSize uint64 = 30

// MinCommitteeSizeFloor is the smallest Params.MinCommitteeSize Validate ever accepts, on any
// chain-id: a bootstrap committee of zero would leave no genesis validator at all.
const MinCommitteeSizeFloor uint64 = 1

// DefaultBootstrapDeadlineEpochs is the genesis default for Params.BootstrapDeadlineEpochs: 365
// epochs, matching plans/open-network.md D16's "~12 months" with x/emission's default one-epoch-
// per-day schedule. x/power measures every time-based rule in epochs, not calendar time (see
// Params.BootstrapDeadlineEpochs's doc comment and docs/CHAIN.md), so a chain that configures a
// different epoch length should set this (and RampEpochs/CapHysteresisEpochs) to match its own
// intended calendar duration.
const DefaultBootstrapDeadlineEpochs uint64 = 365

// DefaultRampEpochs is the genesis default for Params.RampEpochs: 30 epochs, matching
// plans/open-network/track-c-chain.md C4's "30-day ramp" under the same one-epoch-per-day
// assumption as DefaultBootstrapDeadlineEpochs.
const DefaultRampEpochs uint64 = 30

// DefaultCapHysteresisEpochs is the genesis default for Params.CapHysteresisEpochs: 30 epochs,
// matching C4's "hysteresis: it returns to 5% only if the count stays below 50 for 30 days".
const DefaultCapHysteresisEpochs uint64 = 30

// DefaultCapStepDownValidatorCount is the genesis default for Params.CapStepDownValidatorCount:
// the cap steps down from 5% to 3% once more than 60 validators are active.
const DefaultCapStepDownValidatorCount uint64 = 60

// DefaultCapStepUpValidatorCount is the genesis default for Params.CapStepUpValidatorCount: the
// cap steps back up to 5% only once the active count has stayed below 50 for CapHysteresisEpochs.
const DefaultCapStepUpValidatorCount uint64 = 50

// DefaultCometPowerScale is the genesis default for Params.CometPowerScale: the fixed-point scale
// keeper.PowerToCometBFT multiplies a validator's power share by. 1,000,000,000 gives six-figure
// precision on the smallest meaningful share (a validator at the 3% cap still resolves to
// 30,000,000 units) while staying far under CometBFT's own MaxTotalVotingPower
// (math.MaxInt64/8 ~= 1.15e18), even summed across a few hundred validators.
const DefaultCometPowerScale int64 = 1_000_000_000

// MaxCometPowerScale bounds Params.CometPowerScale (security review L2): normalized power shares
// (Keeper.normalizedShares) always sum to at most 1, so the total assigned CometBFT power across
// every validator is at most CometPowerScale + (one validator count's worth of the "floor up to 1"
// rounding rule) - comfortably bounded well under CometBFT's own MaxTotalVotingPower
// (math.MaxInt64/8 ~= 1.15e18) even at this ceiling, with orders of magnitude of headroom for
// per-validator rounding regardless of how many validators exist.
const MaxCometPowerScale int64 = 1 << 56

// DefaultMaxRedistributionMultiplier is the genesis default for Params.MaxRedistributionMultiplier:
// 2x, per security review H3(b).
const DefaultMaxRedistributionMultiplier = "2"

// DefaultPreGateLambdaCap is the genesis default for Params.PreGateLambdaCap: 0.95, per security
// review H3(a).
const DefaultPreGateLambdaCap = "0.95"

// DefaultMinDelegationForRewards is the genesis default for Params.MinDelegationForRewards: 1
// ORAMA, per security review M2's documented stopgap.
var DefaultMinDelegationForRewards = math.NewInt(1).MulRaw(1_000_000_000)

// NewParams builds a Params from its fields, applying no defaults.
func NewParams(
	minCommitteeSize uint64,
	bootstrapExitStake math.Int,
	bootstrapDeadlineEpochs uint64,
	capFractionNormal, capFractionReduced math.LegacyDec,
	capStepDownValidatorCount, capStepUpValidatorCount, capHysteresisEpochs uint64,
	rampEpochs uint64,
	forceBondFraction, selfBondCapMultiplier math.LegacyDec,
	minSelfBond math.Int,
	usefulWorkMultiplier math.LegacyDec,
	usefulWorkMultiplierActivated bool,
	cometPowerScale int64,
	maxRedistributionMultiplier math.LegacyDec,
	preGateLambdaCap math.LegacyDec,
	minDelegationForRewards math.Int,
) Params {
	return Params{
		MinCommitteeSize:              minCommitteeSize,
		BootstrapExitStake:            bootstrapExitStake,
		BootstrapDeadlineEpochs:       bootstrapDeadlineEpochs,
		CapFractionNormal:             capFractionNormal,
		CapFractionReduced:            capFractionReduced,
		CapStepDownValidatorCount:     capStepDownValidatorCount,
		CapStepUpValidatorCount:       capStepUpValidatorCount,
		CapHysteresisEpochs:           capHysteresisEpochs,
		RampEpochs:                    rampEpochs,
		ForceBondFraction:             forceBondFraction,
		SelfBondCapMultiplier:         selfBondCapMultiplier,
		MinSelfBond:                   minSelfBond,
		UsefulWorkMultiplier:          usefulWorkMultiplier,
		UsefulWorkMultiplierActivated: usefulWorkMultiplierActivated,
		CometPowerScale:               cometPowerScale,
		MaxRedistributionMultiplier:   maxRedistributionMultiplier,
		PreGateLambdaCap:              preGateLambdaCap,
		MinDelegationForRewards:       minDelegationForRewards,
	}
}

// DefaultParams returns x/power's genesis-default Params: a production-sized (30-member) bootstrap
// committee floor, a 5%/3% cap stepping at 60/50 validators with a 30-epoch hysteresis, a 30-epoch
// new-validator ramp, a 12-month (365-epoch) bootstrap deadline, 50% force-bonding up to 2x a
// 1,000 ORAMA minimum self-bond, M coded at 1.0 and not activated, redistribution bounded at 2x a
// validator's raw stake share, lambda held at 0.95 pre-handover-gate, and a 1 ORAMA minimum
// delegation.
func DefaultParams() Params {
	return NewParams(
		ProductionMinCommitteeSize,
		math.NewInt(271_000).MulRaw(1_000_000_000), // P1: ~5% of year-1 emission, in norama.
		DefaultBootstrapDeadlineEpochs,
		math.LegacyNewDecWithPrec(5, 2), // 5%
		math.LegacyNewDecWithPrec(3, 2), // 3%
		DefaultCapStepDownValidatorCount,
		DefaultCapStepUpValidatorCount,
		DefaultCapHysteresisEpochs,
		DefaultRampEpochs,
		math.LegacyNewDecWithPrec(50, 2), // 50%
		math.LegacyNewDec(2),             // 2x
		math.NewInt(1_000).MulRaw(1_000_000_000),
		math.LegacyOneDec(),
		false,
		DefaultCometPowerScale,
		math.LegacyMustNewDecFromStr(DefaultMaxRedistributionMultiplier),
		math.LegacyMustNewDecFromStr(DefaultPreGateLambdaCap),
		DefaultMinDelegationForRewards,
	)
}

// Validate checks Params for internal consistency. It does not check MinCommitteeSize against the
// production floor (ProductionMinCommitteeSize) or the genesis bootstrap committee's own size
// against MinCommitteeSize: both depend on the chain-id and the genesis committee list, which
// Params alone does not have access to, so both are checked instead by
// keeper.Keeper.InitGenesis (see keeper.checkCommitteeSizeChainIDGate).
func (p Params) Validate() error {
	if p.MinCommitteeSize < MinCommitteeSizeFloor {
		return fmt.Errorf("min_committee_size must be at least %d, got %d", MinCommitteeSizeFloor, p.MinCommitteeSize)
	}
	if p.BootstrapExitStake.IsNil() || p.BootstrapExitStake.IsNegative() {
		return fmt.Errorf("bootstrap_exit_stake must be a non-negative integer")
	}
	if p.BootstrapDeadlineEpochs == 0 {
		return fmt.Errorf("bootstrap_deadline_epochs must be positive, got 0")
	}
	if err := validateFraction("cap_fraction_normal", p.CapFractionNormal); err != nil {
		return err
	}
	if err := validateFraction("cap_fraction_reduced", p.CapFractionReduced); err != nil {
		return err
	}
	if p.CapFractionReduced.GT(p.CapFractionNormal) {
		return fmt.Errorf("cap_fraction_reduced (%s) must be at most cap_fraction_normal (%s)", p.CapFractionReduced, p.CapFractionNormal)
	}
	if p.CapStepDownValidatorCount == 0 {
		return fmt.Errorf("cap_step_down_validator_count must be positive, got 0")
	}
	if p.CapStepUpValidatorCount == 0 || p.CapStepUpValidatorCount > p.CapStepDownValidatorCount {
		return fmt.Errorf(
			"cap_step_up_validator_count must be in (0, %d], got %d",
			p.CapStepDownValidatorCount, p.CapStepUpValidatorCount,
		)
	}
	if p.CapHysteresisEpochs == 0 {
		return fmt.Errorf("cap_hysteresis_epochs must be positive, got 0")
	}
	if p.RampEpochs == 0 {
		return fmt.Errorf("ramp_epochs must be positive, got 0")
	}
	if err := validateFraction("force_bond_fraction", p.ForceBondFraction); err != nil {
		return err
	}
	if !p.ForceBondFraction.IsPositive() {
		// security review L5: a zero force_bond_fraction would mean a committee member never has
		// to put anything at stake, defeating the entire point of the bootstrap committee having
		// "something to lose" (plans/open-network/track-c-chain.md C4).
		return fmt.Errorf("force_bond_fraction must be positive, got %s", p.ForceBondFraction)
	}
	if !p.SelfBondCapMultiplier.IsPositive() {
		return fmt.Errorf("self_bond_cap_multiplier must be positive, got %s", p.SelfBondCapMultiplier)
	}
	if p.MinSelfBond.IsNil() || !p.MinSelfBond.IsPositive() {
		return fmt.Errorf("min_self_bond must be a positive integer")
	}
	if p.UsefulWorkMultiplier.IsNil() {
		return fmt.Errorf("useful_work_multiplier must be set")
	}
	if p.UsefulWorkMultiplier.LT(math.LegacyNewDecWithPrec(75, 2)) || p.UsefulWorkMultiplier.GT(math.LegacyNewDecWithPrec(125, 2)) {
		return fmt.Errorf("useful_work_multiplier must be in [0.75, 1.25], got %s", p.UsefulWorkMultiplier)
	}
	if p.CometPowerScale <= 0 {
		return fmt.Errorf("comet_power_scale must be positive, got %d", p.CometPowerScale)
	}
	if p.CometPowerScale > MaxCometPowerScale {
		return fmt.Errorf("comet_power_scale must be at most %d, got %d", MaxCometPowerScale, p.CometPowerScale)
	}
	if p.MaxRedistributionMultiplier.IsNil() || !p.MaxRedistributionMultiplier.IsPositive() {
		return fmt.Errorf("max_redistribution_multiplier must be a positive decimal")
	}
	if p.PreGateLambdaCap.IsNil() || p.PreGateLambdaCap.IsNegative() || p.PreGateLambdaCap.GT(math.LegacyOneDec()) {
		return fmt.Errorf("pre_gate_lambda_cap must be in [0, 1], got %s", p.PreGateLambdaCap)
	}
	if p.MinDelegationForRewards.IsNil() || p.MinDelegationForRewards.IsNegative() {
		return fmt.Errorf("min_delegation_for_rewards must be a non-negative integer")
	}
	return nil
}

// validateFraction checks that a named Dec field is set and within [0, 1].
func validateFraction(name string, d math.LegacyDec) error {
	if d.IsNil() {
		return fmt.Errorf("%s must be set", name)
	}
	if d.IsNegative() || d.GT(math.LegacyOneDec()) {
		return fmt.Errorf("%s must be in [0, 1], got %s", name, d)
	}
	return nil
}
