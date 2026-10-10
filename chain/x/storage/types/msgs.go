package types

import (
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/piece"
)

func (m *MsgCreateDeal) ValidateBasic() error {
	if _, err := sdk.AccAddressFromBech32(m.Signer); err != nil {
		return fmt.Errorf("invalid signer: %w", err)
	}
	if m.Granter != "" {
		if _, err := sdk.AccAddressFromBech32(m.Granter); err != nil {
			return fmt.Errorf("invalid granter: %w", err)
		}
	}
	if m.Class != DealClass_DEAL_CLASS_PRIVATE && m.Class != DealClass_DEAL_CLASS_PUBLIC_PIN {
		return fmt.Errorf("user deals must be PRIVATE or PUBLIC_PIN, got %s", m.Class)
	}
	if len(m.DealNonce) != NonceLen {
		return fmt.Errorf("deal_nonce must be %d bytes, got %d", NonceLen, len(m.DealNonce))
	}
	if err := validID("repair_delegate", m.RepairDelegate, true); err != nil {
		return err
	}
	if m.Replicas < MinReplicas || m.Replicas > MaxReplicas {
		return fmt.Errorf("replicas must be in [%d, %d], got %d", MinReplicas, MaxReplicas, m.Replicas)
	}
	if m.PricePerEpoch.IsNil() || !m.PricePerEpoch.IsPositive() {
		return fmt.Errorf("price_per_epoch must be positive")
	}
	if m.DurationEpochs == 0 || m.DurationEpochs > MaxDurationEpochs {
		return fmt.Errorf("duration_epochs must be in [1, %d], got %d", MaxDurationEpochs, m.DurationEpochs)
	}
	switch m.Class {
	case DealClass_DEAL_CLASS_PRIVATE:
		if uint32(len(m.Pieces)) != m.Replicas {
			return fmt.Errorf("PRIVATE deals need one piece commitment per replica, got %d for %d replicas", len(m.Pieces), m.Replicas)
		}
	case DealClass_DEAL_CLASS_PUBLIC_PIN:
		if len(m.Pieces) != 1 {
			return fmt.Errorf("PUBLIC_PIN deals need exactly one piece commitment, got %d", len(m.Pieces))
		}
	}
	for i := range m.Pieces {
		if err := m.Pieces[i].CheckShape(); err != nil {
			return fmt.Errorf("piece %d: %w", i, err)
		}
	}
	return nil
}

func (m *MsgCreateDeal) GetSigners() []sdk.AccAddress {
	return []sdk.AccAddress{mustAcc(m.Signer)}
}

func (m *MsgExtendDeal) ValidateBasic() error {
	if _, err := sdk.AccAddressFromBech32(m.Signer); err != nil {
		return fmt.Errorf("invalid signer: %w", err)
	}
	if m.DealId == 0 {
		return fmt.Errorf("deal_id is required")
	}
	if m.ExtraEpochs == 0 || m.ExtraEpochs > MaxDurationEpochs {
		return fmt.Errorf("extra_epochs must be in [1, %d], got %d", MaxDurationEpochs, m.ExtraEpochs)
	}
	return nil
}

func (m *MsgExtendDeal) GetSigners() []sdk.AccAddress {
	return []sdk.AccAddress{mustAcc(m.Signer)}
}

func (m *MsgGrantDealAuthorization) ValidateBasic() error {
	if _, err := sdk.AccAddressFromBech32(m.Signer); err != nil {
		return fmt.Errorf("invalid signer: %w", err)
	}
	if _, err := sdk.AccAddressFromBech32(m.Grantee); err != nil {
		return fmt.Errorf("invalid grantee: %w", err)
	}
	if m.Signer == m.Grantee {
		return fmt.Errorf("granter and grantee must differ")
	}
	if m.SpendLimit.IsNil() || !m.SpendLimit.IsPositive() {
		return fmt.Errorf("spend_limit must be positive")
	}
	if m.MaxPieceBytes == 0 {
		return fmt.Errorf("max_piece_bytes must be positive")
	}
	if m.MaxDurationEpochs == 0 || m.MaxDurationEpochs > MaxDurationEpochs {
		return fmt.Errorf("max_duration_epochs must be in [1, %d]", MaxDurationEpochs)
	}
	if m.Replicas < MinReplicas || m.Replicas > MaxReplicas {
		return fmt.Errorf("grant replicas must be in [%d, %d], got %d", MinReplicas, MaxReplicas, m.Replicas)
	}
	return nil
}

func (m *MsgGrantDealAuthorization) GetSigners() []sdk.AccAddress {
	return []sdk.AccAddress{mustAcc(m.Signer)}
}

func (m *MsgRevokeDealAuthorization) ValidateBasic() error {
	if _, err := sdk.AccAddressFromBech32(m.Signer); err != nil {
		return fmt.Errorf("invalid signer: %w", err)
	}
	if _, err := sdk.AccAddressFromBech32(m.Grantee); err != nil {
		return fmt.Errorf("invalid grantee: %w", err)
	}
	return nil
}

func (m *MsgRevokeDealAuthorization) GetSigners() []sdk.AccAddress {
	return []sdk.AccAddress{mustAcc(m.Signer)}
}

func (m *MsgAcceptDeal) ValidateBasic() error {
	return validateHotMsg(m.Signer, m.NodeId, m.DealId)
}

func (m *MsgAcceptDeal) GetSigners() []sdk.AccAddress {
	return []sdk.AccAddress{mustAcc(m.Signer)}
}

func (m *MsgDeclineDeal) ValidateBasic() error {
	return validateHotMsg(m.Signer, m.NodeId, m.DealId)
}

func (m *MsgDeclineDeal) GetSigners() []sdk.AccAddress {
	return []sdk.AccAddress{mustAcc(m.Signer)}
}

func (m *MsgSubmitProofs) ValidateBasic() error {
	if _, err := sdk.AccAddressFromBech32(m.Signer); err != nil {
		return fmt.Errorf("invalid signer: %w", err)
	}
	if err := validID("node_id", m.NodeId, false); err != nil {
		return err
	}
	if len(m.Proofs) == 0 {
		return fmt.Errorf("at least one proof is required")
	}
	for i := range m.Proofs {
		if m.Proofs[i].DealId == 0 {
			return fmt.Errorf("proof %d has no deal_id", i)
		}
		if len(m.Proofs[i].Leaf) != piece.LeafSize {
			return fmt.Errorf("proof %d leaf is %d bytes, want %d", i, len(m.Proofs[i].Leaf), piece.LeafSize)
		}
	}
	return nil
}

func (m *MsgSubmitProofs) GetSigners() []sdk.AccAddress {
	return []sdk.AccAddress{mustAcc(m.Signer)}
}

func (m *MsgReleaseReplica) ValidateBasic() error {
	if err := validateHotMsg(m.Signer, m.NodeId, m.DealId); err != nil {
		return err
	}
	if m.Reason != ReleaseReason_RELEASE_REASON_LEGAL {
		return fmt.Errorf("release reason must be LEGAL")
	}
	return nil
}

func (m *MsgReleaseReplica) GetSigners() []sdk.AccAddress {
	return []sdk.AccAddress{mustAcc(m.Signer)}
}

func validateHotMsg(signer, nodeID string, dealID uint64) error {
	if _, err := sdk.AccAddressFromBech32(signer); err != nil {
		return fmt.Errorf("invalid signer: %w", err)
	}
	if err := validID("node_id", nodeID, false); err != nil {
		return err
	}
	if dealID == 0 {
		return fmt.Errorf("deal_id is required")
	}
	return nil
}

func validID(name, id string, allowEmpty bool) error {
	if id == "" {
		if allowEmpty {
			return nil
		}
		return fmt.Errorf("%s is required", name)
	}
	if len(id) > MaxNodeIDLen {
		return fmt.Errorf("%s is longer than %d bytes", name, MaxNodeIDLen)
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.' || r == '_' || r == ':' || r == '-':
		default:
			return fmt.Errorf("%s contains %q", name, r)
		}
	}
	return nil
}

func mustAcc(s string) sdk.AccAddress {
	a, err := sdk.AccAddressFromBech32(s)
	if err != nil {
		panic(fmt.Errorf("invalid signer address %q: %w", s, err))
	}
	return a
}

// CheckShape checks a commitment's internal counts. It does not know the bytes,
// so it cannot recompute the root.
func (c PieceCommitment) CheckShape() error {
	if len(c.Root) != RootLen {
		return fmt.Errorf("root must be %d bytes, got %d", RootLen, len(c.Root))
	}
	if c.RealLeafCount == 0 {
		return fmt.Errorf("real_leaf_count must be positive")
	}
	if c.PieceBytes == 0 {
		return fmt.Errorf("piece_bytes must be positive")
	}
	if piece.RealLeafCount(int(c.PieceBytes)) != c.RealLeafCount {
		return fmt.Errorf("piece_bytes %d implies %d leaves, commitment says %d", c.PieceBytes, piece.RealLeafCount(int(c.PieceBytes)), c.RealLeafCount)
	}
	if piece.PaddedLeafCount(c.RealLeafCount) != c.PaddedLeafCount {
		return fmt.Errorf("padded_leaf_count %d does not match real count %d", c.PaddedLeafCount, c.RealLeafCount)
	}
	return nil
}

// MinBytes returns an error when the piece is under the module minimum.
func (c PieceCommitment) MinBytes(min uint64) error {
	if err := c.CheckShape(); err != nil {
		return err
	}
	if c.PieceBytes < min {
		return fmt.Errorf("piece is %d bytes, minimum is %d", c.PieceBytes, min)
	}
	return nil
}
