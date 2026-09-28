package types

import (
	"fmt"
	"math/bits"

	"cosmossdk.io/math"
)

// GiB is one gibibyte. Declared capacity and bond_per_gib are denominated
// in bytes and norama-per-GiB.
const GiB uint64 = 1 << 30

// CapacityClass is the free-capacity bucket for a STORAGE node.
// Class 0 is unused (zero free bytes are not indexed). Otherwise the class
// is bits.Len64(free), so each bucket is a half-open power-of-two range.
func CapacityClass(free uint64) uint32 {
	if free == 0 {
		return 0
	}
	return uint32(bits.Len64(free))
}

// BackedCapacity returns the STORAGE bytes a bond can declare.
// A zero bond is the probation cap (C2). A positive bond backs
// bond * GiB / bond_per_gib bytes, truncated. A result that does not fit
// in uint64 covers every representable declaration.
func BackedCapacity(storageBond math.Int, p Params) (uint64, error) {
	if storageBond.IsNil() || storageBond.IsNegative() {
		return 0, fmt.Errorf("storage bond must be non-negative")
	}
	if err := p.Validate(); err != nil {
		return 0, fmt.Errorf("params: %w", err)
	}
	if storageBond.IsZero() {
		return p.ProbationCapacityBytes, nil
	}
	bytes := storageBond.Mul(math.NewIntFromUint64(GiB)).Quo(p.BondPerGib)
	if !bytes.IsUint64() {
		return ^uint64(0), nil
	}
	return bytes.Uint64(), nil
}
