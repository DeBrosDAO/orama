package types

import (
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

var _ sdk.Msg = (*MsgFaucet)(nil)

// ValidateBasic checks MsgFaucet's stateless fields: both addresses are well-formed bech32 and
// the amount is a positive integer. The faucet's limits depend on Params and are checked in
// Msg.Faucet.
func (m *MsgFaucet) ValidateBasic() error {
	if _, err := sdk.AccAddressFromBech32(m.Signer); err != nil {
		return fmt.Errorf("invalid signer %q: %w", m.Signer, err)
	}
	if _, err := sdk.AccAddressFromBech32(m.Recipient); err != nil {
		return fmt.Errorf("invalid recipient %q: %w", m.Recipient, err)
	}
	if m.Amount.IsNil() || !m.Amount.IsPositive() {
		return fmt.Errorf("amount must be positive")
	}
	return nil
}
