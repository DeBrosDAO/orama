package indexer

import (
	"time"

	"cosmossdk.io/math"
)

// Everything below is an aggregate the follower computes once, as it ingests blocks, and stores.
// The API only reads them. Amounts are norama as decimal strings.

// EpochRow is one closed emission epoch. An epoch spans StartHeight to EndHeight; EndHeight is the
// block whose BeginBlock closed it. StartTime is the epoch start the chain records, which is the
// time of the block that closed the epoch before (StartHeight - 1).
//
// The minted, burned and fee figures are the change in the chain's cumulative counters between the
// state after the previous epoch's closing block and the state after this one's: what happened in
// the epoch's blocks, not what the schedule allows. Partial is true for the first epoch an index
// tracks when it began inside that epoch: its start is the chain's, its totals cover only the
// blocks the index saw.
type EpochRow struct {
	Epoch       uint64    `json:"epoch"`
	StartHeight int64     `json:"start_height"`
	StartTime   time.Time `json:"start_time"`
	EndHeight   int64     `json:"end_height"`
	EndTime     time.Time `json:"end_time"`
	Blocks      int64     `json:"blocks"`
	Partial     bool      `json:"partial"`
	// MaxMintable is the schedule's ceiling for the epoch (x/emission ScheduleAt).
	MaxMintable string `json:"max_mintable"`
	// MintedValidators is the validator and delegator share, minted when the epoch closed. The
	// chain pays it to earnings accounts without an event or query per recipient, so the split
	// between validators and delegators is not available.
	MintedValidators  string `json:"minted_validators"`
	MintedService     string `json:"minted_service"`
	MintedDevelopment string `json:"minted_development"`
	MintedFaucet      string `json:"minted_faucet"`
	MintedTotal       string `json:"minted_total"`
	Burned            string `json:"burned"`
	FeesCollected     string `json:"fees_collected"`
	FeesBurned        string `json:"fees_burned"`
	FeesDistributed   string `json:"fees_distributed"`
	// RewardsCredited is what x/power credited to earnings accounts when the epoch closed (the
	// bank transfers from x/power to x/fees in the closing block's events), and RewardsForceBonded
	// what it bonded into bootstrap committee members' self-delegations instead.
	RewardsCredited    string `json:"rewards_credited"`
	RewardsForceBonded string `json:"rewards_force_bonded"`
}

// SupplyPoint is the supply breakdown after an epoch's closing block. Every figure is a query at
// EndHeight. Circulating is TotalSupply less what every protocol module account holds
// (Bonded + Unbonding + EarningsPools + StorageEscrow + OtherProtocol).
type SupplyPoint struct {
	Epoch         uint64    `json:"epoch"`
	Height        int64     `json:"height"`
	Time          time.Time `json:"time"`
	TotalSupply   string    `json:"total_supply"`
	MintedToDate  string    `json:"minted_to_date"`
	BurnedToDate  string    `json:"burned_to_date"`
	GenesisSupply string    `json:"genesis_supply"`
	Bonded        string    `json:"bonded"`
	// Unbonding is the staking not-bonded pool: tokens of unbonding delegations and of validators
	// that are unbonding or unbonded.
	Unbonding     string `json:"unbonding"`
	EarningsPools string `json:"earnings_pools"`
	StorageEscrow string `json:"storage_escrow"`
	OtherProtocol string `json:"other_protocol"`
	Circulating   string `json:"circulating"`
}

// Validator statuses, as x/staking names them.
const (
	StatusBonded    = "bonded"
	StatusUnbonding = "unbonding"
	StatusUnbonded  = "unbonded"
)

// ValidatorInfo is what the index knows of one validator as of the last epoch it closed (or the
// index's first block): who it is, its status and power.
type ValidatorInfo struct {
	Operator         string `json:"operator"`
	ConsensusAddress string `json:"consensus_address"`
	Moniker          string `json:"moniker"`
	Status           string `json:"status"`
	Jailed           bool   `json:"jailed"`
	Tokens           string `json:"tokens"`
	CometPower       int64  `json:"comet_power"`
	UpdatedEpoch     uint64 `json:"updated_epoch"`
}

// ValidatorEpoch is one validator in one closed epoch. SignedBlocks and MissedBlocks count the
// commits the epoch's blocks carried: a block the validator did not sign for commit (absent, or a
// nil vote) is missed. UptimeBps is signed over signed plus missed, in basis points.
type ValidatorEpoch struct {
	Epoch            uint64 `json:"epoch"`
	Operator         string `json:"operator"`
	ConsensusAddress string `json:"consensus_address"`
	CometPower       int64  `json:"comet_power"`
	SignedBlocks     uint64 `json:"signed_blocks"`
	MissedBlocks     uint64 `json:"missed_blocks"`
	UptimeBps        uint64 `json:"uptime_bps"`
	Slashes          uint64 `json:"slashes"`
	SlashedBurned    string `json:"slashed_burned"`
	Jailed           bool   `json:"jailed"`
	Status           string `json:"status"`
}

// Slash is one slashing of a validator. Reason is x/slashing's own attribute value
// (double_sign, missing_signature or unspecified).
type Slash struct {
	Height int64     `json:"height"`
	Time   time.Time `json:"time"`
	Epoch  uint64    `json:"epoch"`
	Reason string    `json:"reason"`
	Power  int64     `json:"power"`
	Burned string    `json:"burned"`
}

// JailPeriod is one stretch of a validator being jailed. JailedHeight 0 means it was jailed before
// the index's first block; UnjailedHeight 0 means it is still jailed.
type JailPeriod struct {
	JailedHeight   int64      `json:"jailed_height"`
	JailedTime     *time.Time `json:"jailed_time,omitempty"`
	Reason         string     `json:"reason,omitempty"`
	UnjailedHeight int64      `json:"unjailed_height"`
	UnjailedTime   *time.Time `json:"unjailed_time,omitempty"`
}

// Bucket is the transactions of one UTC hour, day or week (weeks start on Monday). Burned is
// base fee burned by transactions, in norama.
type Bucket struct {
	Start  time.Time `json:"start"`
	Txs    uint64    `json:"txs"`
	Failed uint64    `json:"failed"`
	Burned string    `json:"burned"`
}

// cumulatives are the chain's running totals the epoch deltas are taken from.
type cumulatives struct {
	Minted          math.Int `json:"minted"`
	Burned          math.Int `json:"burned"`
	Development     math.Int `json:"development"`
	Service         math.Int `json:"service"`
	Faucet          math.Int `json:"faucet"`
	FeesCollected   math.Int `json:"fees_collected"`
	FeesBurned      math.Int `json:"fees_burned"`
	FeesDistributed math.Int `json:"fees_distributed"`
}

// watch is where the aggregator stands: the last block it folded in, the epoch in progress and the
// rule that closes it. Height makes folding a block idempotent.
type watch struct {
	Height        int64       `json:"height"`
	Epoch         uint64      `json:"epoch"`
	StartHeight   int64       `json:"start_height"`
	StartUnixNano int64       `json:"start_unix_nano"`
	DurationSec   int64       `json:"duration_seconds"`
	MinBlocks     uint64      `json:"min_blocks"`
	Partial       bool        `json:"partial"`
	Prev          cumulatives `json:"prev"`
	// ValSetHash is the hash of the validator set that signed Height, which the next block's
	// commit is signed by.
	ValSetHash []byte `json:"valset_hash"`
}

// counters are a validator's tallies inside the epoch in progress.
type counters struct {
	Signed  uint64   `json:"signed"`
	Missed  uint64   `json:"missed"`
	Slashes uint64   `json:"slashes"`
	Burned  math.Int `json:"burned"`
}
