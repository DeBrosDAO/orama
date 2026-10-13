package types

import "cosmossdk.io/collections"

const (
	// ModuleName is the name of x/power.
	ModuleName = "power"

	// StoreKey is the store key for x/power.
	StoreKey = ModuleName
)

var (
	// ParamsKey is the collections key for the module's genesis-only Params.
	ParamsKey = collections.NewPrefix(0)
	// LambdaStateKey is the collections key for the module's mutable LambdaState.
	LambdaStateKey = collections.NewPrefix(1)
	// CapStateKey is the collections key for the module's mutable CapState.
	CapStateKey = collections.NewPrefix(2)
	// BootstrapCommitteePrefix is the collections key prefix for the fixed genesis bootstrap
	// committee, keyed by operator address.
	BootstrapCommitteePrefix = collections.NewPrefix(3)
	// RampRecordsPrefix is the collections key prefix for each validator's ramp activation epoch,
	// keyed by operator address.
	RampRecordsPrefix = collections.NewPrefix(4)
	// LastPowerPrefix is the collections key prefix for each validator's last-assigned CometBFT
	// integer power, keyed by operator address.
	LastPowerPrefix = collections.NewPrefix(5)
	// CommitteeSelfBondPrefix is the collections key prefix for each committee member's
	// cumulative force-bonded amount, keyed by operator address.
	CommitteeSelfBondPrefix = collections.NewPrefix(6)
	// LastPubKeyPrefix is the collections key prefix for the raw consensus pubkey bytes last used
	// in a ValidatorUpdate for each operator address, so a later removal update (power 0) can reuse
	// the exact identity CometBFT already knows, keyed by operator address.
	LastPubKeyPrefix = collections.NewPrefix(7)
	// RampAdmittedPrefix is the token amount whose ramp has finished.
	RampAdmittedPrefix = collections.NewPrefix(14)
	// RampExcessPrefix is the token amount still ramping on top of RampAdmitted.
	RampExcessPrefix = collections.NewPrefix(15)
	// RampExcessEpochPrefix is the epoch the current excess started ramping.
	RampExcessEpochPrefix = collections.NewPrefix(16)
)
