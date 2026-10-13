package types

import (
	"fmt"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	cnfttypes "github.com/DeBrosOfficial/network/chain/x/cnft/types"
)

var (
	_ sdk.Msg = (*MsgList)(nil)
	_ sdk.Msg = (*MsgCancelListing)(nil)
	_ sdk.Msg = (*MsgBid)(nil)
	_ sdk.Msg = (*MsgCancelBid)(nil)
	_ sdk.Msg = (*MsgSettle)(nil)
)

func validateBech32(field, addr string) error {
	if _, err := sdk.AccAddressFromBech32(addr); err != nil {
		return fmt.Errorf("invalid %s %q: %w", field, addr, err)
	}
	return nil
}

func positive(field string, amount math.Int) error {
	if amount.IsNil() || !amount.IsPositive() {
		return fmt.Errorf("%s must be positive", field)
	}
	return nil
}

func (m *MsgList) ValidateBasic() error {
	if err := validateBech32("seller", m.Seller); err != nil {
		return err
	}
	if m.TreeId == 0 {
		return fmt.Errorf("tree id is required")
	}
	if err := m.Leaf.Validate(); err != nil {
		return err
	}
	if err := m.Proof.ValidateBasic(); err != nil {
		return err
	}
	return positive("price", m.Price)
}

func (m *MsgList) GetSigners() []sdk.AccAddress { return signersOf(m.Seller) }

func (m *MsgCancelListing) ValidateBasic() error {
	if err := validateBech32("seller", m.Seller); err != nil {
		return err
	}
	if m.ListingId == 0 {
		return fmt.Errorf("listing id is required")
	}
	return nil
}

func (m *MsgCancelListing) GetSigners() []sdk.AccAddress { return signersOf(m.Seller) }

func (m *MsgBid) ValidateBasic() error {
	if err := validateBech32("bidder", m.Bidder); err != nil {
		return err
	}
	if m.ListingId == 0 {
		return fmt.Errorf("listing id is required")
	}
	return positive("amount", m.Amount)
}

func (m *MsgBid) GetSigners() []sdk.AccAddress { return signersOf(m.Bidder) }

func (m *MsgCancelBid) ValidateBasic() error {
	if err := validateBech32("bidder", m.Bidder); err != nil {
		return err
	}
	if m.ListingId == 0 || m.BidId == 0 {
		return fmt.Errorf("listing id and bid id are required")
	}
	return nil
}

func (m *MsgCancelBid) GetSigners() []sdk.AccAddress { return signersOf(m.Bidder) }

func (m *MsgSettle) ValidateBasic() error {
	if err := validateBech32("signer", m.Signer); err != nil {
		return err
	}
	if m.ListingId == 0 {
		return fmt.Errorf("listing id is required")
	}
	if err := m.Leaf.Validate(); err != nil {
		return err
	}
	return m.Proof.ValidateBasic()
}

func (m *MsgSettle) GetSigners() []sdk.AccAddress { return signersOf(m.Signer) }

func signersOf(bech32 string) []sdk.AccAddress {
	addr, err := sdk.AccAddressFromBech32(bech32)
	if err != nil {
		return nil
	}
	return []sdk.AccAddress{addr}
}

// LeafOwnerMatches reports whether the leaf's owner is addr.
func LeafOwnerMatches(leaf cnfttypes.Leaf, addr sdk.AccAddress) (bool, error) {
	owner, err := sdk.AccAddressFromBech32(leaf.Owner)
	if err != nil {
		return false, fmt.Errorf("leaf owner: %w", err)
	}
	return owner.Equals(addr), nil
}
