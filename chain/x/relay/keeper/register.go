package keeper

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"cosmossdk.io/collections"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/relay/types"
)

// relayClaim is the validated content of a registration or an update: the relay as it will be stored,
// before the checks that depend on what is registered already.
type relayClaim struct {
	operator    sdk.AccAddress
	relay       types.Relay
	fingerprint []byte
}

// registerRelay records a relay's RSA digest after the node's ed25519 identity
// has signed it. The per-relay bond is not escrowed here: bond locking belongs
// to x/nodes (C6), which this module does not import. The ed25519 key is
// snapshotted from the node's "relay" binding; MsgUpdateRelay refreshes it
// after a rotation, and this module does not watch binding changes by itself.
func (k Keeper) registerRelay(ctx sdk.Context, msg *types.MsgRegisterRelay) error {
	claim, err := k.checkRelayClaim(ctx, "register relay", msg.Operator, msg.NodeId, relayProof{fingerprint: msg.RsaFingerprint, ed25519Signature: msg.Ed25519Signature, rsaPublicKey: msg.RsaPublicKey, rsaSignature: msg.RsaSignature}, msg.Exit)
	if err != nil {
		return err
	}
	if _, found, err := k.getRelay(ctx, claim.fingerprint); err != nil {
		return err
	} else if found {
		return fmt.Errorf("register relay: fingerprint already registered")
	}
	if has, err := k.NodeIndex.Has(ctx, msg.NodeId); err != nil {
		return fmt.Errorf("register relay: failed to check node %s: %w", msg.NodeId, err)
	} else if has {
		return fmt.Errorf("register relay: node %s is already registered; MsgUpdateRelay rotates its fingerprint", msg.NodeId)
	}
	if err := k.storeRelay(ctx, claim.relay); err != nil {
		return fmt.Errorf("register relay: %w", err)
	}
	ctx.EventManager().EmitEvent(sdk.NewEvent(types.EventTypeRelayRegistered, relayAttributes(claim.relay)...))
	return nil
}

// updateRelay replaces the fingerprint of the relay registered on msg.NodeId. The node's current
// "relay" ed25519 binding is read again, so it is also how a relay follows a key rotation done with
// MsgUpdateNode on x/nodes. The jail flag is kept; the old fingerprint stops being a registered relay
// (its earlier payouts stay under it).
func (k Keeper) updateRelay(ctx sdk.Context, msg *types.MsgUpdateRelay) error {
	claim, err := k.checkRelayClaim(ctx, "update relay", msg.Operator, msg.NodeId, relayProof{fingerprint: msg.RsaFingerprint, ed25519Signature: msg.Ed25519Signature, rsaPublicKey: msg.RsaPublicKey, rsaSignature: msg.RsaSignature}, msg.Exit)
	if err != nil {
		return err
	}
	oldFingerprint, err := k.NodeIndex.Get(ctx, msg.NodeId)
	if errors.Is(err, collections.ErrNotFound) {
		return fmt.Errorf("update relay: node %s: %w", msg.NodeId, types.ErrRelayNotRegistered)
	}
	if err != nil {
		return fmt.Errorf("update relay: failed to load the relay of node %s: %w", msg.NodeId, err)
	}
	old, found, err := k.getRelay(ctx, oldFingerprint)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("update relay: node %s indexes fingerprint %X that is not stored", msg.NodeId, oldFingerprint)
	}
	if old.Operator != claim.relay.Operator {
		return fmt.Errorf("update relay: operator does not own the relay of node %s", msg.NodeId)
	}
	if string(oldFingerprint) != string(claim.fingerprint) {
		if _, taken, err := k.getRelay(ctx, claim.fingerprint); err != nil {
			return err
		} else if taken {
			return fmt.Errorf("update relay: fingerprint already registered")
		}
		if err := k.removeRelay(ctx, old); err != nil {
			return fmt.Errorf("update relay: %w", err)
		}
	}
	claim.relay.Jailed = old.Jailed
	if err := k.storeRelay(ctx, claim.relay); err != nil {
		return fmt.Errorf("update relay: %w", err)
	}
	attrs := append(relayAttributes(claim.relay), sdk.NewAttribute(types.AttributeKeyPreviousRSAFingerprint, fingerprintHex(oldFingerprint)))
	ctx.EventManager().EmitEvent(sdk.NewEvent(types.EventTypeRelayUpdated, attrs...))
	return nil
}

// relayProof is what a registration or an update offers as proof of the fingerprint: the node's
// ed25519 cross-signature over it, and the RSA identity key behind it with that key's signature.
type relayProof struct {
	fingerprint      []byte
	ed25519Signature []byte
	rsaPublicKey     []byte
	rsaSignature     []byte
}

// checkRelayClaim validates what a registration and an update share: the shapes, the operator's
// ownership of the node, that the operator is not a reporter, the node's ed25519 cross-signature over
// the fingerprint and the RSA identity's own proof of it. what names the message in errors.
func (k Keeper) checkRelayClaim(ctx sdk.Context, what, operatorAddr, nodeID string, proof relayProof, exit bool) (relayClaim, error) {
	rsaFingerprint := proof.fingerprint
	if nodeID == "" {
		return relayClaim{}, fmt.Errorf("%s: node id is empty", what)
	}
	if len(rsaFingerprint) != types.RSAFingerprintLen {
		return relayClaim{}, fmt.Errorf("%s: rsa fingerprint must be %d bytes", what, types.RSAFingerprintLen)
	}
	operator, err := sdk.AccAddressFromBech32(operatorAddr)
	if err != nil {
		return relayClaim{}, fmt.Errorf("%s: invalid operator: %w", what, err)
	}
	if reporter, err := k.isReporter(ctx, operator.String()); err != nil {
		return relayClaim{}, err
	} else if reporter {
		return relayClaim{}, fmt.Errorf("%s: operator %s: %w", what, operator, types.ErrReporterOperatesRelay)
	}
	pub, boundOperator, network, err := k.nodes.RelayBinding(ctx, nodeID)
	if err != nil {
		return relayClaim{}, fmt.Errorf("%s: node %s: %w", what, nodeID, err)
	}
	if len(pub) != types.Ed25519PubLen {
		return relayClaim{}, fmt.Errorf("%s: node %s ed25519 key must be %d bytes", what, nodeID, types.Ed25519PubLen)
	}
	if !boundOperator.Equals(operator) {
		return relayClaim{}, fmt.Errorf("%s: operator does not own node %s", what, nodeID)
	}
	if !types.VerifyCrossCert(pub, nodeID, rsaFingerprint, exit, proof.ed25519Signature) {
		return relayClaim{}, fmt.Errorf("%s: %w", what, types.ErrCrossCertMismatch)
	}
	if err := types.VerifyRSAProof(rsaFingerprint, proof.rsaPublicKey, pub, nodeID, exit, proof.rsaSignature); err != nil {
		return relayClaim{}, fmt.Errorf("%s: %w", what, err)
	}
	prefix16, err := types.RelayPrefix16(network)
	if err != nil {
		return relayClaim{}, fmt.Errorf("%s: node %s: %w", what, nodeID, err)
	}
	fingerprint := append([]byte(nil), rsaFingerprint...)
	return relayClaim{
		operator:    operator,
		fingerprint: fingerprint,
		relay: types.Relay{
			NodeId:         nodeID,
			RsaFingerprint: fingerprint,
			Ed25519Id:      append([]byte(nil), pub...),
			Operator:       operator.String(),
			Prefix16:       prefix16,
			Exit:           exit,
		},
	}, nil
}

// storeRelay writes a relay and the two indexes that follow it.
func (k Keeper) storeRelay(ctx sdk.Context, relay types.Relay) error {
	if err := k.Relays.Set(ctx, relay.RsaFingerprint, relay); err != nil {
		return fmt.Errorf("failed to store relay: %w", err)
	}
	if err := k.NodeIndex.Set(ctx, relay.NodeId, relay.RsaFingerprint); err != nil {
		return fmt.Errorf("failed to index node %s: %w", relay.NodeId, err)
	}
	if err := k.OperatorIndex.Set(ctx, collections.Join(relay.Operator, relay.RsaFingerprint)); err != nil {
		return fmt.Errorf("failed to index operator %s: %w", relay.Operator, err)
	}
	return nil
}

// removeRelay deletes a relay record and its operator index entry. The node index and the last
// payout are left to the caller: an update re-points the node, and a payout belongs to the old
// fingerprint's history.
func (k Keeper) removeRelay(ctx sdk.Context, relay types.Relay) error {
	if err := k.Relays.Remove(ctx, relay.RsaFingerprint); err != nil {
		return fmt.Errorf("failed to remove relay %X: %w", relay.RsaFingerprint, err)
	}
	if err := k.OperatorIndex.Remove(ctx, collections.Join(relay.Operator, relay.RsaFingerprint)); err != nil {
		return fmt.Errorf("failed to remove relay %X from operator %s: %w", relay.RsaFingerprint, relay.Operator, err)
	}
	return nil
}

func relayAttributes(relay types.Relay) []sdk.Attribute {
	return []sdk.Attribute{
		sdk.NewAttribute(types.AttributeKeyNodeID, relay.NodeId),
		sdk.NewAttribute(types.AttributeKeyOperator, relay.Operator),
		sdk.NewAttribute(types.AttributeKeyRSAFingerprint, fingerprintHex(relay.RsaFingerprint)),
		sdk.NewAttribute(types.AttributeKeyEd25519ID, hex.EncodeToString(relay.Ed25519Id)),
		sdk.NewAttribute(types.AttributeKeyExit, fmt.Sprint(relay.Exit)),
	}
}

// fingerprintHex is a fingerprint as Tor writes it: 40 upper-case hex digits.
func fingerprintHex(fingerprint []byte) string {
	return strings.ToUpper(hex.EncodeToString(fingerprint))
}
