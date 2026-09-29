package types

import (
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

var (
	_ sdk.Msg              = (*MsgAttest)(nil)
	_ sdk.Msg              = (*MsgAttachReplicas)(nil)
	_ sdk.HasValidateBasic = (*MsgAttest)(nil)
	_ sdk.HasValidateBasic = (*MsgAttachReplicas)(nil)
	_ sdk.LegacyMsg        = (*MsgAttest)(nil)
	_ sdk.LegacyMsg        = (*MsgAttachReplicas)(nil)
)

// ValidateBasic checks MsgAttest without reading state. The signer is Archiver.
func (m *MsgAttest) ValidateBasic() error {
	if m == nil {
		return fmt.Errorf("nil MsgAttest")
	}
	_, err := ValidateAttestation(m.Archiver, m.NodeId, m.StartHeight, m.EndHeight, m.BundleCid, m.BundleHash, m.MerkleRoot)
	return err
}

// GetSigners returns the archiver. Ante signing uses the proto signer option;
// this matches it for the legacy Msg interface.
func (m *MsgAttest) GetSigners() []sdk.AccAddress {
	addr, err := sdk.AccAddressFromBech32(m.Archiver)
	if err != nil {
		panic(err)
	}
	return []sdk.AccAddress{addr}
}

// ValidateBasic checks MsgAttachReplicas without reading state. The signer is Archiver.
func (m *MsgAttachReplicas) ValidateBasic() error {
	if m == nil {
		return fmt.Errorf("nil MsgAttachReplicas")
	}
	_, err := ValidateAttach(m.Archiver, m.NodeId, m.StartHeight, m.EndHeight, m.DealIds)
	return err
}

// GetSigners returns the archiver who is recording the deal ids.
func (m *MsgAttachReplicas) GetSigners() []sdk.AccAddress {
	addr, err := sdk.AccAddressFromBech32(m.Archiver)
	if err != nil {
		panic(err)
	}
	return []sdk.AccAddress{addr}
}
