package types

import (
	"bytes"
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

var (
	_ sdk.Msg = (*MsgCreateCollection)(nil)
	_ sdk.Msg = (*MsgCreateTree)(nil)
	_ sdk.Msg = (*MsgMint)(nil)
	_ sdk.Msg = (*MsgTransfer)(nil)
	_ sdk.Msg = (*MsgBurn)(nil)
	_ sdk.Msg = (*MsgUpdateMetadata)(nil)
	_ sdk.Msg = (*MsgDecompress)(nil)
	_ sdk.Msg = (*MsgCompress)(nil)
	_ sdk.Msg = (*MsgRecordSnapshot)(nil)
)

func validateBech32(field, addr string) error {
	if _, err := sdk.AccAddressFromBech32(addr); err != nil {
		return fmt.Errorf("invalid %s %q: %w", field, addr, err)
	}
	return nil
}

func validateDelegate(addr string) error {
	if addr == "" {
		return nil
	}
	return validateBech32("delegate", addr)
}

func validateCID(field, cid string) error {
	if cid == "" || len(cid) > MaxCIDLen {
		return fmt.Errorf("%s must be 1..%d bytes", field, MaxCIDLen)
	}
	return nil
}

func (leaf Leaf) Validate() error {
	if len(leaf.AssetId) != HashSize {
		return fmt.Errorf("asset_id must be %d bytes", HashSize)
	}
	if err := validateBech32("owner", leaf.Owner); err != nil {
		return err
	}
	if err := validateDelegate(leaf.Delegate); err != nil {
		return err
	}
	if err := validateCID("metadata_cid", leaf.MetadataCid); err != nil {
		return err
	}
	if len(leaf.CreatorHash) != HashSize {
		return fmt.Errorf("creator_hash must be %d bytes", HashSize)
	}
	if leaf.HashId != HashIDSHA256 {
		return fmt.Errorf("unsupported hash_id %d", leaf.HashId)
	}
	return nil
}

func (p MerkleProof) ValidateBasic() error {
	if len(p.Root) != HashSize {
		return fmt.Errorf("proof root must be %d bytes", HashSize)
	}
	if len(p.Siblings) > int(MaxDepth) {
		return fmt.Errorf("proof has %d siblings, max %d", len(p.Siblings), MaxDepth)
	}
	for i, sibling := range p.Siblings {
		if len(sibling) != HashSize {
			return fmt.Errorf("proof sibling %d must be %d bytes", i, HashSize)
		}
	}
	return nil
}

func (m *MsgCreateCollection) ValidateBasic() error {
	if err := validateBech32("creator", m.Creator); err != nil {
		return err
	}
	if m.Name == "" || len(m.Name) > MaxNameLen {
		return fmt.Errorf("collection name must be 1..%d bytes", MaxNameLen)
	}
	if m.RoyaltyBps > RoyaltyBasisPoints {
		return fmt.Errorf("royalty_bps %d exceeds %d", m.RoyaltyBps, RoyaltyBasisPoints)
	}
	return nil
}

func (m *MsgCreateCollection) GetSigners() []sdk.AccAddress {
	return signersOf(m.Creator)
}

func (m *MsgCreateTree) ValidateBasic() error {
	if err := validateBech32("creator", m.Creator); err != nil {
		return err
	}
	if m.CollectionId == 0 {
		return fmt.Errorf("collection id is required")
	}
	return ValidateTreeShape(m.Depth, m.Buffer, m.Canopy)
}

func (m *MsgCreateTree) GetSigners() []sdk.AccAddress {
	return signersOf(m.Creator)
}

func (m *MsgMint) ValidateBasic() error {
	if err := validateBech32("creator", m.Creator); err != nil {
		return err
	}
	if m.TreeId == 0 {
		return fmt.Errorf("tree id is required")
	}
	if len(m.Root) != HashSize {
		return fmt.Errorf("root must be %d bytes", HashSize)
	}
	if len(m.Leaves) == 0 || len(m.Leaves) > MaxMintBatch {
		return fmt.Errorf("mint batch must contain 1..%d leaves", MaxMintBatch)
	}
	seen := make(map[string]struct{}, len(m.Leaves))
	for i, leaf := range m.Leaves {
		if len(leaf.AssetId) != HashSize {
			return fmt.Errorf("leaf %d asset_id must be %d bytes", i, HashSize)
		}
		if err := validateBech32("owner", leaf.Owner); err != nil {
			return fmt.Errorf("leaf %d: %w", i, err)
		}
		if err := validateDelegate(leaf.Delegate); err != nil {
			return fmt.Errorf("leaf %d: %w", i, err)
		}
		if err := validateCID("metadata_cid", leaf.MetadataCid); err != nil {
			return fmt.Errorf("leaf %d: %w", i, err)
		}
		key := string(leaf.AssetId)
		if _, ok := seen[key]; ok {
			return fmt.Errorf("duplicate asset_id in mint batch")
		}
		seen[key] = struct{}{}
	}
	return nil
}

func (m *MsgMint) GetSigners() []sdk.AccAddress {
	return signersOf(m.Creator)
}

func (m *MsgTransfer) ValidateBasic() error {
	if err := validateBech32("signer", m.Signer); err != nil {
		return err
	}
	if m.TreeId == 0 {
		return fmt.Errorf("tree id is required")
	}
	if err := m.Current.Validate(); err != nil {
		return err
	}
	if err := validateBech32("new_owner", m.NewOwner); err != nil {
		return err
	}
	if err := validateDelegate(m.NewDelegate); err != nil {
		return err
	}
	if err := m.Proof.ValidateBasic(); err != nil {
		return err
	}
	if same, err := SameAccount(m.Current.Owner, m.NewOwner); err != nil {
		return err
	} else if same && m.NewDelegate == m.Current.Delegate {
		return fmt.Errorf("transfer does not change the leaf")
	}
	return nil
}

func (m *MsgTransfer) GetSigners() []sdk.AccAddress {
	return signersOf(m.Signer)
}

func (m *MsgBurn) ValidateBasic() error {
	if err := validateBech32("signer", m.Signer); err != nil {
		return err
	}
	if m.TreeId == 0 {
		return fmt.Errorf("tree id is required")
	}
	if err := m.Current.Validate(); err != nil {
		return err
	}
	return m.Proof.ValidateBasic()
}

func (m *MsgBurn) GetSigners() []sdk.AccAddress {
	return signersOf(m.Signer)
}

func (m *MsgUpdateMetadata) ValidateBasic() error {
	if err := validateBech32("signer", m.Signer); err != nil {
		return err
	}
	if m.TreeId == 0 {
		return fmt.Errorf("tree id is required")
	}
	if err := m.Current.Validate(); err != nil {
		return err
	}
	if err := validateCID("new_metadata_cid", m.NewMetadataCid); err != nil {
		return err
	}
	return m.Proof.ValidateBasic()
}

func (m *MsgUpdateMetadata) GetSigners() []sdk.AccAddress {
	return signersOf(m.Signer)
}

func (m *MsgDecompress) ValidateBasic() error {
	if err := validateBech32("owner", m.Owner); err != nil {
		return err
	}
	if m.TreeId == 0 {
		return fmt.Errorf("tree id is required")
	}
	if err := m.Current.Validate(); err != nil {
		return err
	}
	if same, err := SameAccount(m.Owner, m.Current.Owner); err != nil {
		return err
	} else if !same {
		return fmt.Errorf("decompress signer is not the leaf owner")
	}
	return m.Proof.ValidateBasic()
}

func (m *MsgDecompress) GetSigners() []sdk.AccAddress {
	return signersOf(m.Owner)
}

func (m *MsgCompress) ValidateBasic() error {
	if err := validateBech32("owner", m.Owner); err != nil {
		return err
	}
	if len(m.AssetId) != HashSize {
		return fmt.Errorf("asset_id must be %d bytes", HashSize)
	}
	if len(m.Root) != HashSize {
		return fmt.Errorf("root must be %d bytes", HashSize)
	}
	return nil
}

func (m *MsgCompress) GetSigners() []sdk.AccAddress {
	return signersOf(m.Owner)
}

func (m *MsgRecordSnapshot) ValidateBasic() error {
	if err := validateBech32("creator", m.Creator); err != nil {
		return err
	}
	if m.TreeId == 0 {
		return fmt.Errorf("tree id is required")
	}
	return validateCID("cid", m.Cid)
}

func (m *MsgRecordSnapshot) GetSigners() []sdk.AccAddress {
	return signersOf(m.Creator)
}

func signersOf(bech32 string) []sdk.AccAddress {
	addr, err := sdk.AccAddressFromBech32(bech32)
	if err != nil {
		return nil
	}
	return []sdk.AccAddress{addr}
}

// SameAccount reports whether two bech32 account strings are the same account.
func SameAccount(a, b string) (bool, error) {
	aa, err := sdk.AccAddressFromBech32(a)
	if err != nil {
		return false, fmt.Errorf("address %q: %w", a, err)
	}
	bb, err := sdk.AccAddressFromBech32(b)
	if err != nil {
		return false, fmt.Errorf("address %q: %w", b, err)
	}
	return aa.Equals(bb), nil
}

// ZeroLeaf is the empty leaf written by burn and decompress.
func ZeroLeaf() []byte {
	return make([]byte, HashSize)
}

// AssetIDsEqual reports whether two asset ids are the same 32-byte value.
func AssetIDsEqual(a, b []byte) bool {
	return bytes.Equal(a, b)
}
