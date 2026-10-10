package types

import "cosmossdk.io/errors"

// The faucet's typed refusals. Each is returned by Msg.Faucet before anything is minted.
var (
	// ErrFaucetProduction is returned when the faucet is called on a production chain-id.
	ErrFaucetProduction = errors.Register(ModuleName, 1, "the faucet runs only on a devnet, stagenet or localnet chain-id")

	// ErrFaucetDisabled is returned when Params.FaucetEnabled is false.
	ErrFaucetDisabled = errors.Register(ModuleName, 2, "the faucet is disabled (faucet_enabled is false)")

	// ErrFaucetAmount is returned for a drip that is not positive or exceeds faucet_max_drip.
	ErrFaucetAmount = errors.Register(ModuleName, 3, "faucet amount must be positive and at most faucet_max_drip")

	// ErrFaucetRecipient is returned for a recipient that cannot receive a drip: a malformed
	// address, or a module or otherwise blocked account.
	ErrFaucetRecipient = errors.Register(ModuleName, 4, "faucet recipient is invalid or a blocked address")

	// ErrFaucetCooldown is returned when the recipient drew within faucet_recipient_cooldown_seconds.
	ErrFaucetCooldown = errors.Register(ModuleName, 5, "faucet recipient is still within its cooldown")

	// ErrFaucetEpochCap is returned when a drip would take the epoch's faucet mint past faucet_epoch_cap.
	ErrFaucetEpochCap = errors.Register(ModuleName, 6, "faucet epoch cap exceeded")
)
