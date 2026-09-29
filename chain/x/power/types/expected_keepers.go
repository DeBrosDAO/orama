package types

import (
	"context"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
)

// StakingKeeper is the subset of x/staking's keeper x/power needs. x/power never creates,
// bonds or unbonds a validator itself (plans/open-network/track-c-chain.md C4: "staking keeps
// bonds/delegations"): it only reads the resulting validator set and stake, and - for a bootstrap
// committee member who has already created a real validator by self-bonding - delegates
// force-bonded reward amounts on that member's behalf, exactly as MsgDelegate would.
type StakingKeeper interface {
	// GetValidator returns the validator at addr, or a "not found" error (types.ErrNoValidatorFound
	// upstream) if none has been created yet - the normal state for a bootstrap committee member who
	// has not yet self-bonded (see Keeper.committeeValidator).
	GetValidator(ctx context.Context, addr sdk.ValAddress) (stakingtypes.Validator, error)
	// GetBondedValidatorsByPower returns every currently bonded validator, in x/staking's own
	// deterministic power-then-address order. This is x/power's universe of "outsider" (non-
	// bootstrap, real-stake) validators, and len(...) is the active_validator_count the cap
	// hysteresis rule (types.UpdateCapState) reacts to.
	GetBondedValidatorsByPower(ctx context.Context) ([]stakingtypes.Validator, error)
	// GetValidatorDelegations returns every delegation to valAddr, in a deterministic order (a
	// prefix scan over x/staking's own delegation store), used to pay each delegator its pro-rata
	// share of a validator's reward (Keeper.distributeValidatorReward).
	GetValidatorDelegations(ctx context.Context, valAddr sdk.ValAddress) ([]stakingtypes.Delegation, error)
	// GetDelegation returns delAddr's delegation to valAddr, used to read a committee member's
	// current self-delegation when checking the self_bond_cap_multiplier ceiling.
	GetDelegation(ctx context.Context, delAddr sdk.AccAddress, valAddr sdk.ValAddress) (stakingtypes.Delegation, error)
	// Delegate performs a self-delegation on a committee member's behalf, funded from delAddr's own
	// account (Keeper.forceBondCommitteeReward always sends the coins there first). It is the same
	// keeper method MsgDelegate uses.
	Delegate(ctx context.Context, delAddr sdk.AccAddress, bondAmt math.Int, tokenSrc stakingtypes.BondStatus, validator stakingtypes.Validator, subtractAccount bool) (math.LegacyDec, error)
	// BondDenom returns x/staking's bond denomination (norama).
	BondDenom(ctx context.Context) (string, error)
	// SetValidator and SetValidatorByConsAddr are used only by InitGenesis, to give every
	// bootstrap committee member a real (Bonded, zero-token) stakingtypes.Validator record from
	// genesis - deliberately NOT indexed by power (Keeper.InitGenesis never calls
	// SetValidatorByPowerIndex for one), so it never appears in GetBondedValidatorsByPower and
	// contributes no C_i of its own until it is actually delegated to. This is required for x/slashing's
	// existing downtime/jailing machinery to work at all for a committee member: its BeginBlocker
	// calls IsValidatorJailed (and so GetValidatorByConsAddr) for every block signer, which panics
	// the whole chain with "validator does not exist" if no record exists - see docs/CHAIN.md.
	SetValidator(ctx context.Context, validator stakingtypes.Validator) error
	SetValidatorByConsAddr(ctx context.Context, validator stakingtypes.Validator) error
	// Hooks returns x/staking's registered hooks (notably x/slashing's, which creates a
	// validator's initial signing-info bookkeeping and registers its pubkey). InitGenesis fires
	// AfterValidatorCreated for each committee member, exactly as stakingkeeper.InitGenesis does
	// for a gentx-declared genesis validator.
	Hooks() stakingtypes.StakingHooks
}

// SlashingKeeper is the subset of x/slashing's keeper x/power needs to check whether a committee
// member's consensus address has been permanently tombstoned (security review B2/H2): a tombstone
// removes a seat for good, since IsTombstoned never reverts to false once set.
type SlashingKeeper interface {
	IsTombstoned(ctx context.Context, consAddr sdk.ConsAddress) bool
}

// BankKeeper is the subset of x/bank's keeper x/power needs to move an already-minted reward
// (received from x/emission) into a committee member's own account before self-delegating it.
// x/power never mints or burns.
type BankKeeper interface {
	SendCoinsFromModuleToModule(ctx context.Context, senderModule, recipientModule string, amt sdk.Coins) error
	SendCoinsFromModuleToAccount(ctx context.Context, senderModule string, recipientAddr sdk.AccAddress, amt sdk.Coins) error
	// GetBalance is read only by the module-account invariant (Keeper.CheckInvariants).
	GetBalance(ctx context.Context, addr sdk.AccAddress, denom string) sdk.Coin
}

// EarningsKeeper is the subset of x/fees's keeper x/power needs to credit a validator's or
// delegator's reward share (plans/open-network/track-c-chain.md C2: "every protocol payout lands
// in the recipient's earnings account"). Implemented by x/fees/keeper.Keeper. CreditEarnings moves
// amt from senderModule's own account into x/fees' module account itself (x/power never needs to
// know x/fees' module name) and credits it to addr's earnings ledger entry.
type EarningsKeeper interface {
	CreditEarnings(ctx context.Context, senderModule string, addr sdk.AccAddress, amt sdk.Coin) error
}

// EmissionKeeper is the subset of x/emission's keeper x/power needs. x/power deliberately measures
// every one of its own time-based rules (the bootstrap deadline, the cap hysteresis window, the
// new-validator ramp) in x/emission epochs rather than calendar time or block height, so they
// track the same notion of "a day" x/emission's own halving schedule uses, on any chain regardless
// of its configured epoch length (see docs/CHAIN.md).
type EmissionKeeper interface {
	// CurrentEpoch returns the epoch number currently in progress.
	CurrentEpoch(ctx context.Context) (uint64, error)
}
