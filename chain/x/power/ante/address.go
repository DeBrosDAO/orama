package ante

import (
	errorsmod "cosmossdk.io/errors"

	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
)

// canonicalAccount returns the checksummed bech32 account string. The SDK
// accepts any case, and two casings of one address must not be tracked as two
// delegations.
func canonicalAccount(bech32 string) (string, error) {
	addr, err := sdk.AccAddressFromBech32(bech32)
	if err != nil {
		return "", errorsmod.Wrap(sdkerrors.ErrInvalidAddress, err.Error())
	}
	return addr.String(), nil
}

// canonicalValidator returns the checksummed bech32 validator-operator string.
func canonicalValidator(bech32 string) (string, error) {
	addr, err := sdk.ValAddressFromBech32(bech32)
	if err != nil {
		return "", errorsmod.Wrap(sdkerrors.ErrInvalidAddress, err.Error())
	}
	return addr.String(), nil
}
