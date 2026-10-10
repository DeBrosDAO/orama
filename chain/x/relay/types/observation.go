package types

import (
	"fmt"

	"cosmossdk.io/math"
)

// MaxConsensusWeight bounds one reported consensus weight. A weight is a payout
// unit capped by per_relay_cap, so a bound of 2^62 never excludes a real report,
// and it keeps every sum and product settlement forms far from math.Int's
// 256-bit overflow (which would halt the chain in the end block).
var MaxConsensusWeight = math.NewIntFromUint64(1 << 62)

// Validate checks one observation's shape. It does not check that the
// fingerprint is registered.
func (o RelayObservation) Validate() error {
	if len(o.RsaFingerprint) != RSAFingerprintLen {
		return fmt.Errorf("rsa fingerprint must be %d bytes", RSAFingerprintLen)
	}
	if len(o.Ed25519Id) != Ed25519PubLen {
		return fmt.Errorf("ed25519 id must be %d bytes", Ed25519PubLen)
	}
	if o.ConsensusWeight.IsNil() || o.ConsensusWeight.IsNegative() || o.ConsensusWeight.GT(MaxConsensusWeight) {
		return fmt.Errorf("consensus weight must be in [0, %s]", MaxConsensusWeight)
	}
	if o.UptimeFraction.IsNil() || o.UptimeFraction.IsNegative() || o.UptimeFraction.GT(math.LegacyOneDec()) {
		return fmt.Errorf("uptime must be in [0, 1]")
	}
	return nil
}
