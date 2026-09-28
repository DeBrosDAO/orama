package keeper

import (
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/relay/types"
)

// registerRelay records a relay's RSA digest after the node's ed25519 identity
// has signed it. The per-relay bond is not escrowed here: bond locking belongs
// to x/nodes (C6), which this module does not import. The ed25519 key is
// snapshotted; MsgUpdateNode on x/nodes is the rotation path, and this module
// does not watch later binding changes.
func (k Keeper) registerRelay(ctx sdk.Context, msg *types.MsgRegisterRelay) error {
	if msg.NodeId == "" {
		return fmt.Errorf("register relay: node id is empty")
	}
	if len(msg.RsaFingerprint) != types.RSAFingerprintLen {
		return fmt.Errorf("register relay: rsa fingerprint must be %d bytes", types.RSAFingerprintLen)
	}
	operator, err := sdk.AccAddressFromBech32(msg.Operator)
	if err != nil {
		return fmt.Errorf("register relay: invalid operator: %w", err)
	}
	pub, boundOperator, ipv4, err := k.nodes.RelayBinding(ctx, msg.NodeId)
	if err != nil {
		return fmt.Errorf("register relay: node %s: %w", msg.NodeId, err)
	}
	if len(pub) != types.Ed25519PubLen {
		return fmt.Errorf("register relay: node %s ed25519 key must be %d bytes", msg.NodeId, types.Ed25519PubLen)
	}
	if !boundOperator.Equals(operator) {
		return fmt.Errorf("register relay: operator does not own node %s", msg.NodeId)
	}
	if !types.VerifyCrossCert(pub, msg.NodeId, msg.RsaFingerprint, msg.Ed25519Signature) {
		return fmt.Errorf("register relay: %w", types.ErrCrossCertMismatch)
	}
	prefix16, err := types.CanonicalPrefix16(ipv4)
	if err != nil {
		return fmt.Errorf("register relay: node %s: %w", msg.NodeId, err)
	}

	if _, found, err := k.getRelay(ctx, msg.RsaFingerprint); err != nil {
		return err
	} else if found {
		return fmt.Errorf("register relay: fingerprint already registered")
	}
	if has, err := k.NodeIndex.Has(ctx, msg.NodeId); err != nil {
		return fmt.Errorf("register relay: failed to check node %s: %w", msg.NodeId, err)
	} else if has {
		return fmt.Errorf("register relay: node %s is already registered", msg.NodeId)
	}

	fingerprint := append([]byte(nil), msg.RsaFingerprint...)
	relay := types.Relay{
		NodeId:         msg.NodeId,
		RsaFingerprint: fingerprint,
		Ed25519Id:      append([]byte(nil), pub...),
		Operator:       operator.String(),
		Prefix16:       prefix16,
		Exit:           msg.Exit,
	}
	if err := k.Relays.Set(ctx, fingerprint, relay); err != nil {
		return fmt.Errorf("register relay: failed to store relay: %w", err)
	}
	if err := k.NodeIndex.Set(ctx, msg.NodeId, fingerprint); err != nil {
		return fmt.Errorf("register relay: failed to index node %s: %w", msg.NodeId, err)
	}
	return nil
}
