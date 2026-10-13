package types

const (
	// EventTypeWithdrawEarnings is emitted when an account moves its earnings to its bank balance.
	EventTypeWithdrawEarnings = "withdraw_earnings"

	// AttributeSigner is the account that owned and received the withdrawn earnings.
	AttributeSigner = "signer"
	// AttributeAmount is the withdrawn amount in norama.
	AttributeAmount = "amount"
	// AttributeRemaining is the signer's earnings balance after the withdrawal.
	AttributeRemaining = "remaining_earnings"
)
