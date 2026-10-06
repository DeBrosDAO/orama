package keeper

import (
	"context"
	"fmt"

	"github.com/DeBrosOfficial/network/chain/x/relay/types"
)

// JailRelay marks a relay jailed so settlement pays it nothing. This is the
// only jailing path. A governance caller (x/houses, which this module does
// not import) uses it after a passed structural proposal with BadExit
// evidence. Evidence is the caller's responsibility. There is no automatic
// slashing, and no message jails a relay.
func (k Keeper) JailRelay(ctx context.Context, rsaFingerprint []byte) error {
	if len(rsaFingerprint) != types.RSAFingerprintLen {
		return fmt.Errorf("jail relay: rsa fingerprint must be %d bytes", types.RSAFingerprintLen)
	}
	relay, found, err := k.getRelay(ctx, rsaFingerprint)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("jail relay: relay is not registered")
	}
	if relay.Jailed {
		return nil
	}
	relay.Jailed = true
	if err := k.Relays.Set(ctx, rsaFingerprint, relay); err != nil {
		return fmt.Errorf("jail relay: failed to store relay: %w", err)
	}
	return nil
}
