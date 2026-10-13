package types

// Event type and attribute keys emitted by Msg.Faucet.
const (
	EventTypeFaucet = "faucet"

	AttributeKeyRecipient = "recipient"
	AttributeKeyAmount    = "amount"
	AttributeKeySigner    = "signer"
)

// EventTypeEpochClosed is emitted once by the block that closes an emission epoch (Keeper.AdvanceBlock,
// in BeginBlock), after the epoch's mint and payout and the advance of the epoch state, so it is in
// the block's finalize events with the power_validator_reward events of the same close. It says
// where the epoch boundary is: the closing block, the epoch it closed and how many blocks it held.
// The boundary follows from params the chain can change (the epoch duration and the minimum block
// count), so a reader that did not watch them cannot work it out from the rule, and the epoch
// counter is state that a node stops keeping after its pruning window. A block that closes nothing
// emits nothing.
const EventTypeEpochClosed = "emission_epoch_closed"

const (
	// AttributeKeyEpoch is the number (decimal) of the epoch that closed.
	AttributeKeyEpoch = "epoch"
	// AttributeKeyBlocks is how many blocks the closed epoch held (decimal), the closing block included.
	AttributeKeyBlocks = "blocks"
	// AttributeKeyStartUnixNano is the BFT time the closed epoch began at, in Unix nanoseconds (decimal).
	AttributeKeyStartUnixNano = "start_unix_nano"
)
