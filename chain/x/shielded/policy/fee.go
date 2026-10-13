package policy

import (
	"errors"

	"cosmossdk.io/math"
)

// ErrFeeUnpaid means the bundle's value balance does not cover its fee.
// A signer-less shielded transfer pays the fee from that balance.
var ErrFeeUnpaid = errors.New("shielded bundle does not cover its fee")

// BundlePaysFee checks valueIn = valueOut + fee. The fee is burned or tipped
// by the caller. This function only checks the balance.
func BundlePaysFee(valueIn, valueOut, fee math.Int) error {
	if !valueIn.IsPositive() && !valueIn.IsZero() || valueOut.IsNegative() || fee.IsNegative() {
		return ErrAmount
	}
	if !valueIn.Equal(valueOut.Add(fee)) {
		return ErrFeeUnpaid
	}
	return nil
}
