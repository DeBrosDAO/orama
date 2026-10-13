package types

// Events x/power emits when an epoch's validator share is paid (Keeper.DistributeEpochRewards). They
// carry the validator and delegator the chain credited, which no other event or query does: the
// credit itself is a bank transfer from x/power to x/fees with no recipient.
const (
	// EventTypeValidatorReward is emitted once per validator paid in an epoch close, with the split
	// of its reward. The epoch is the one the emitting block closes.
	EventTypeValidatorReward = "power_validator_reward"
	// EventTypeDelegatorReward is emitted for each delegator other than the validator's operator
	// that was credited a positive amount.
	EventTypeDelegatorReward = "power_delegator_reward"

	// AttributeKeyValidator is the validator operator address (orama valoper bech32).
	AttributeKeyValidator = "validator"
	// AttributeKeyDelegator is the delegator account that was credited.
	AttributeKeyDelegator = "delegator"
	// AttributeKeyAmount is the validator's whole reward, or a delegator's credit, in norama.
	AttributeKeyAmount = "amount"
	// AttributeKeyCommission is the validator's commission (amount times its commission rate,
	// floored; zero when it had no delegator shares), in norama.
	AttributeKeyCommission = "commission"
	// AttributeKeyOperatorShare is what the operator's own account was due: amount less the delegators'
	// credits. It includes the commission, the operator's pro-rata cut and the flooring remainder.
	AttributeKeyOperatorShare = "operator_share"
	// AttributeKeyDelegators is the sum of the credits to delegators other than the operator.
	AttributeKeyDelegators = "delegators"
	// AttributeKeyForceBonded is the part of the operator's share bonded into a bootstrap committee
	// member's self-delegation instead of credited to earnings.
	AttributeKeyForceBonded = "force_bonded"
)
