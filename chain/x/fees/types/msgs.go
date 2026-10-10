package types

import (
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

var _ sdk.Msg = &MsgWithdrawEarnings{}

// ValidateBasic checks MsgWithdrawEarnings statelessly: the signer is a valid address and the
// amount is a positive integer. Whether the signer's earnings cover the amount is state, checked
// by the keeper.
func (msg MsgWithdrawEarnings) ValidateBasic() error {
	if _, err := sdk.AccAddressFromBech32(msg.Signer); err != nil {
		return fmt.Errorf("withdraw earnings: signer %q: %w", msg.Signer, err)
	}
	if msg.Amount.IsNil() || !msg.Amount.IsPositive() {
		return fmt.Errorf("withdraw earnings: amount must be a positive integer, got %s", msg.Amount)
	}
	return nil
}
