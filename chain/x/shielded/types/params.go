package types

import (
	"fmt"

	"cosmossdk.io/math"

	"github.com/DeBrosOfficial/network/chain/x/shielded/pool"
)

// The genesis defaults. None of them is measured: C12a's spec text does not exist yet, and C0-4
// measured no verify cost, so these are named placeholders until the G1 sign-off, like P2.
const (
	// DefaultAnchorWindowBlocks is 24 hours at 6-second blocks. It is the state cost of one
	// 32-byte root and one height per block, pruned as the window moves.
	DefaultAnchorWindowBlocks uint64 = 14_400
	// DefaultActionGas is the gas one action declares. Not measured (C0-4).
	DefaultActionGas uint64 = 250_000
	// DefaultMaxActions bounds the proof work of one bundle.
	DefaultMaxActions uint32 = 16
	// DefaultNullifierFee is 0.001 ORAMA burned per nullifier, in norama.
	DefaultNullifierFee int64 = 1_000_000
	// DefaultMaxFeeTopup is 0.01 ORAMA, in norama.
	DefaultMaxFeeTopup int64 = 10_000_000
	// DefaultQueuePerAddressCap is 100 ORAMA, in norama.
	DefaultQueuePerAddressCap int64 = 100_000_000_000
)

// DefaultParams returns the genesis defaults.
func DefaultParams() Params {
	return Params{
		AnchorWindowBlocks:  DefaultAnchorWindowBlocks,
		NullifierFee:        math.NewInt(DefaultNullifierFee),
		ActionGas:           DefaultActionGas,
		MaxActionsPerBundle: DefaultMaxActions,
		UnshieldFloor:       math.NewInt(pool.UnshieldFloor),
		MaxFeeTopup:         math.NewInt(DefaultMaxFeeTopup),
		QueuePerAddressCap:  math.NewInt(DefaultQueuePerAddressCap),
	}
}

// Validate checks Params for internal consistency.
func (p Params) Validate() error {
	if p.AnchorWindowBlocks == 0 {
		return fmt.Errorf("anchor_window_blocks must be positive")
	}
	if p.ActionGas == 0 {
		return fmt.Errorf("action_gas must be positive")
	}
	if p.MaxActionsPerBundle == 0 {
		return fmt.Errorf("max_actions_per_bundle must be positive")
	}
	for name, v := range map[string]math.Int{
		"nullifier_fee": p.NullifierFee, "unshield_floor": p.UnshieldFloor,
		"max_fee_topup": p.MaxFeeTopup, "queue_per_address_cap": p.QueuePerAddressCap,
	} {
		if v.IsNil() || !v.IsPositive() {
			return fmt.Errorf("%s must be a positive integer", name)
		}
	}
	return nil
}

// TxGas is the gas limit a bundle of n actions must declare and is charged.
func (p Params) TxGas(actions int) uint64 { return p.ActionGas * uint64(actions) }
