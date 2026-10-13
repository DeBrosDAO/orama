package types

// Events x/relay emits so an indexer can attribute relay registrations and pay without reading
// state: settlement credits earnings by a module-to-module transfer that names no relay.
const (
	// EventTypeRelayRegistered is emitted when MsgRegisterRelay records a relay.
	EventTypeRelayRegistered = "relay_registered"
	// EventTypeRelayUpdated is emitted when MsgUpdateRelay replaces a relay's fingerprint.
	EventTypeRelayUpdated = "relay_updated"
	// EventTypeRelayPayout is emitted once per paid relay when an epoch settles, with the relay's
	// gross norama after the caps and the pro-rata scale to the ceiling.
	EventTypeRelayPayout = "relay_payout"
	// EventTypeOperatorPaid is emitted once per paid operator when an epoch settles, with the split
	// of the operator's total: what reached its earnings, what was burned and what funded the archive.
	EventTypeOperatorPaid = "relay_operator_paid"

	// AttributeKeyEpoch is the settled epoch.
	AttributeKeyEpoch = "epoch"
	// AttributeKeyNodeID is the x/nodes node the relay is registered on.
	AttributeKeyNodeID = "node_id"
	// AttributeKeyOperator is the operator account the relay pays.
	AttributeKeyOperator = "operator"
	// AttributeKeyRSAFingerprint is the relay's RSA identity digest as 40 upper-case hex digits.
	AttributeKeyRSAFingerprint = "rsa_fingerprint"
	// AttributeKeyPreviousRSAFingerprint is the fingerprint a MsgUpdateRelay replaced.
	AttributeKeyPreviousRSAFingerprint = "previous_rsa_fingerprint"
	// AttributeKeyEd25519ID is the relay's ed25519 identity as lower-case hex.
	AttributeKeyEd25519ID = "ed25519_id"
	// AttributeKeyExit is the operator's exit declaration.
	AttributeKeyExit = "exit"
	// AttributeKeyAmount is a relay's gross payout, or an operator's total, in norama.
	AttributeKeyAmount = "amount"
	// AttributeKeyEarnings is the part of an operator's total credited to its earnings, in norama.
	AttributeKeyEarnings = "earnings"
	// AttributeKeyBurned is the part of an operator's total that was burned, in norama.
	AttributeKeyBurned = "burned"
	// AttributeKeyArchive is the part of an operator's total that funded the archive, in norama.
	AttributeKeyArchive = "archive"
)
