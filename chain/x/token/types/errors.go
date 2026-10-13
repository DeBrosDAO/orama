package types

import "errors"

var (
	// ErrShieldPowers is returned when a token still holds freeze, permanent
	// delegate, or pause authority. Those powers can never be made shieldable.
	ErrShieldPowers = errors.New("token cannot be marked shieldable while freeze, permanent delegate, or pause powers remain")

	// ErrHookGasCap is returned when a transfer hook consumes more than
	// TransferHookGasCap gas. The transfer does not move tokens.
	ErrHookGasCap = errors.New("transfer hook exceeded gas cap")
)
