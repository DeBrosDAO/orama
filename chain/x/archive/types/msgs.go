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
	_ sdk.Msg              = (*MsgCreateArchiveDeal)(nil)
	_ sdk.HasValidateBasic = (*MsgCreateArchiveDeal)(nil)
	_ sdk.LegacyMsg        = (*MsgCreateArchiveDeal)(nil)
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

// ValidateBasic checks MsgCreateArchiveDeal without reading state. The signer is Archiver.
func (m *MsgCreateArchiveDeal) ValidateBasic() error {
	if m == nil {
		return fmt.Errorf("nil MsgCreateArchiveDeal")
	}
	if _, err := ValidateArchiver(m.Archiver); err != nil {
		return err
	}
	if err := ValidateNodeID(m.NodeId); err != nil {
		return err
	}
	if err := ValidateHeights(m.StartHeight, m.EndHeight); err != nil {
		return err
	}
	if err := ValidateHash("piece_root", m.PieceRoot); err != nil {
		return err
	}
	if m.PieceBytes == 0 || m.RealLeafCount == 0 || m.PaddedLeafCount < m.RealLeafCount {
		return fmt.Errorf("piece needs bytes and leaves, with padded leaves at least the real ones")
	}
	return nil
}

// GetSigners returns the archiver who is asking for the deal.
func (m *MsgCreateArchiveDeal) GetSigners() []sdk.AccAddress {
	addr, err := sdk.AccAddressFromBech32(m.Archiver)
	if err != nil {
		panic(err)
	}
	return []sdk.AccAddress{addr}
}
