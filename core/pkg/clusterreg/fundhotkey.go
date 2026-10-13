package clusterreg

import "fmt"

// FundHotKeyTypeURL is the Any type URL of orama.nodes.v1.MsgFundHotKey.
const FundHotKeyTypeURL = "/orama.nodes.v1.MsgFundHotKey"

// fundSourceBank is orama.nodes.v1.FundSource FUND_SOURCE_BANK, field 4 of MsgFundHotKey. The enum's
// zero, FUND_SOURCE_EARNINGS, is the message's original behaviour and is never written.
const fundSourceBank = 1

// HotKeyFunding is MsgFundHotKey: Amount norama into the fee-only balance of the hot key registered
// on the operator's own node. The target is always that hot key; the message names no destination.
// FromBank takes the amount from the operator's bank balance. Otherwise it comes from the operator's
// earnings, which an operator that has not earned yet does not have.
type HotKeyFunding struct {
	Operator string
	NodeID   string
	Amount   string
	FromBank bool
}

// ValidateFundHotKey checks the operator, the node id and the amount.
func ValidateFundHotKey(f HotKeyFunding) error {
	if _, err := CanonicalAccount(f.Operator); err != nil {
		return fmt.Errorf("operator: %w", err)
	}
	if !idPattern.MatchString(f.NodeID) {
		return fmt.Errorf("node id %q must match %s", f.NodeID, idPattern.String())
	}
	return validateAmount(f.Amount)
}

// EncodeFundHotKey is the protobuf orama.nodes.v1.MsgFundHotKey. The source, field 4, is written
// only for the bank: proto3 omits the enum's zero, so an earnings funding is the bytes it always was.
func EncodeFundHotKey(f HotKeyFunding) []byte {
	out := appendStringField(nil, 1, f.Operator)
	out = appendStringField(out, 2, f.NodeID)
	out = appendStringField(out, 3, f.Amount)
	if f.FromBank {
		out = appendUvarintField(out, 4, fundSourceBank)
	}
	return out
}
