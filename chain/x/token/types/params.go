package types

import (
	"fmt"

	"cosmossdk.io/math"

	"github.com/DeBrosOfficial/network/chain/app/params"
)

const (
	// CreationFee is the genesis-default token creation fee, in norama: 10 ORAMA.
	// plans/open-network.md P4 calls this about $10–25 equivalent and does not
	// fix a norama amount. This constant does not track a dollar price.
	CreationFee int64 = 10 * params.NoramaPerOrama

	// DepositPerByte is the genesis-default metadata deposit, in norama per byte
	// of subdenom + name + symbol + description. P3's ≈0.07 ORAMA/KB is
	// 0.07 * NoramaPerOrama norama per 1024 bytes; integer division is 68359.
	// It is not a price oracle.
	DepositPerByte int64 = (7 * params.NoramaPerOrama / 100) / 1024
)

// NewParams builds a Params from its fields, applying no defaults.
func NewParams(creationFee, depositPerByte math.Int) Params {
	return Params{
		CreationFee:    creationFee,
		DepositPerByte: depositPerByte,
	}
}

// DefaultParams returns x/token's genesis-default Params.
func DefaultParams() Params {
	return NewParams(math.NewInt(CreationFee), math.NewInt(DepositPerByte))
}

// Validate checks Params for internal consistency.
func (p Params) Validate() error {
	if p.CreationFee.IsNil() || !p.CreationFee.IsPositive() {
		return fmt.Errorf("creation_fee must be a positive integer")
	}
	if p.DepositPerByte.IsNil() || !p.DepositPerByte.IsPositive() {
		return fmt.Errorf("deposit_per_byte must be a positive integer")
	}
	return nil
}
