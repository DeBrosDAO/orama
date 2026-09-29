package types

import (
	"encoding/binary"
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"

	nodestypes "github.com/DeBrosOfficial/network/chain/x/nodes/types"
	"github.com/DeBrosOfficial/network/chain/x/shielded/bundle"
)

var (
	_ sdk.Msg = &MsgShieldedTransfer{}
	_ sdk.Msg = &MsgShield{}
	_ sdk.Msg = &MsgShieldEarnings{}
	_ sdk.Msg = &MsgUnshield{}
)

func validateBundleField(b []byte) error {
	if len(b) == 0 || len(b) > bundle.MaxBytes {
		return fmt.Errorf("%w: %d bytes", ErrBundleSize, len(b))
	}
	return nil
}

func validateSigner(signer string) error {
	if _, err := sdk.AccAddressFromBech32(signer); err != nil {
		return fmt.Errorf("signer %q: %w", signer, err)
	}
	return nil
}

// ValidateBasic checks the bundle field and that the signer is the fixed protocol address.
func (msg MsgShieldedTransfer) ValidateBasic() error {
	addr, err := sdk.AccAddressFromBech32(msg.Signer)
	if err != nil {
		return fmt.Errorf("signer %q: %w", msg.Signer, err)
	}
	if !addr.Equals(SignerlessAddress()) {
		return ErrSigner
	}
	return validateBundleField(msg.Bundle)
}

// ValidateBasic checks the signer and the bundle field.
func (msg MsgShield) ValidateBasic() error {
	if err := validateSigner(msg.Signer); err != nil {
		return err
	}
	return validateBundleField(msg.Bundle)
}

// ValidateBasic checks the signer and the bundle field.
func (msg MsgShieldEarnings) ValidateBasic() error {
	if err := validateSigner(msg.Signer); err != nil {
		return err
	}
	return validateBundleField(msg.Bundle)
}

// ValidateBasic checks the signer, the bundle field and that the target has the fields it needs.
func (msg MsgUnshield) ValidateBasic() error {
	if err := validateSigner(msg.Signer); err != nil {
		return err
	}
	if err := validateBundleField(msg.Bundle); err != nil {
		return err
	}
	switch msg.Target {
	case UnshieldTargetBond:
		if _, err := sdk.ValAddressFromBech32(msg.Validator); err != nil {
			return fmt.Errorf("%w: bond target needs a validator operator address: %w", ErrTarget, err)
		}
	case UnshieldTargetNodeBond:
		if err := nodestypes.ValidateID(msg.NodeId); err != nil {
			return fmt.Errorf("%w: node bond target: %w", ErrTarget, err)
		}
		if msg.Role == nodestypes.RoleUnspecified {
			return fmt.Errorf("%w: node bond target needs a role", ErrTarget)
		}
	case UnshieldTargetFeeTopup, UnshieldTargetDeposit, UnshieldTargetContract:
	default:
		return fmt.Errorf("%w: %s", ErrTarget, msg.Target)
	}
	return nil
}

// Binding is what an unshield's signatures must commit to besides the bundle: its signer and its
// target. It is the sighash binding (orchard.Sighash), so a bundle a wallet built for one signer
// and one target is worthless under any other. Without it, anyone who saw the bundle in the
// mempool could submit it as their own unshield and take the funds.
//
//	u8(len(signer)) || signer || u8(target) || u8(len(validator)) || validator
//	    || u16be(len(node_id)) || node_id || u8(role)
//
// Signer and validator are the decoded address bytes.
func (msg MsgUnshield) Binding() ([]byte, error) {
	signer, err := sdk.AccAddressFromBech32(msg.Signer)
	if err != nil {
		return nil, fmt.Errorf("signer %q: %w", msg.Signer, err)
	}
	var validator sdk.ValAddress
	if msg.Validator != "" {
		if validator, err = sdk.ValAddressFromBech32(msg.Validator); err != nil {
			return nil, fmt.Errorf("%w: validator %q: %w", ErrTarget, msg.Validator, err)
		}
	}
	out := []byte{byte(len(signer))}
	out = append(out, signer...)
	out = append(out, byte(msg.Target), byte(len(validator)))
	out = append(out, validator...)
	out = binary.BigEndian.AppendUint16(out, uint16(len(msg.NodeId)))
	out = append(out, msg.NodeId...)
	return append(out, byte(msg.Role)), nil
}
