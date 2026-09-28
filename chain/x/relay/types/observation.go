package types

import (
	"fmt"

	"cosmossdk.io/math"
)

// Validate checks one observation's shape. It does not check that the
// fingerprint is registered.
func (o RelayObservation) Validate() error {
	if len(o.RsaFingerprint) != RSAFingerprintLen {
		return fmt.Errorf("rsa fingerprint must be %d bytes", RSAFingerprintLen)
	}
	if len(o.Ed25519Id) != Ed25519PubLen {
		return fmt.Errorf("ed25519 id must be %d bytes", Ed25519PubLen)
	}
	if o.ConsensusWeight.IsNil() || o.ConsensusWeight.IsNegative() {
		return fmt.Errorf("consensus weight must be non-negative")
	}
	if o.UptimeFraction.IsNil() || o.UptimeFraction.IsNegative() || o.UptimeFraction.GT(math.LegacyOneDec()) {
		return fmt.Errorf("uptime must be in [0, 1]")
	}
	return nil
}
