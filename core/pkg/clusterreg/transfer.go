package clusterreg

import "fmt"

const (
	// SendTypeURL is the Any type URL of cosmos.bank.v1beta1.MsgSend, the public payment.
	SendTypeURL = "/cosmos.bank.v1beta1.MsgSend"
	// WithdrawEarningsTypeURL is the Any type URL of orama.fees.v1.MsgWithdrawEarnings.
	WithdrawEarningsTypeURL = "/orama.fees.v1.MsgWithdrawEarnings"

	// maxAmountDigits bounds an amount of norama before the chain sees it. The whole supply is
	// 19 digits of norama; a bounded number cannot overflow the chain's 256-bit integer.
	maxAmountDigits = 38
)

// validateAmount accepts a positive integer of norama with no leading zero and at most
// maxAmountDigits digits, the text cosmos-sdk's math.Int marshals.
func validateAmount(amount string) error {
	if !positiveInteger(amount) {
		return fmt.Errorf("amount %q must be a positive integer of %s", amount, FeeDenom)
	}
	if len(amount) > maxAmountDigits {
		return fmt.Errorf("amount has %d digits, at most %d", len(amount), maxAmountDigits)
	}
	return nil
}

// EncodeSend is the protobuf cosmos.bank.v1beta1.MsgSend: amount norama from one account to another.
// Both addresses must be canonical orama accounts and may not be the same account.
func EncodeSend(from, to, amount string) ([]byte, error) {
	if _, err := CanonicalAccount(from); err != nil {
		return nil, fmt.Errorf("sender: %w", err)
	}
	if _, err := CanonicalAccount(to); err != nil {
		return nil, fmt.Errorf("recipient: %w", err)
	}
	if from == to {
		return nil, fmt.Errorf("the recipient %s is the sender: a payment to yourself moves nothing", to)
	}
	if err := validateAmount(amount); err != nil {
		return nil, err
	}
	coin := appendStringField(nil, 1, FeeDenom)
	coin = appendStringField(coin, 2, amount)
	msg := appendStringField(nil, 1, from)
	msg = appendStringField(msg, 2, to)
	return appendBytesField(msg, 3, coin), nil
}

// EncodeWithdrawEarnings is the protobuf orama.fees.v1.MsgWithdrawEarnings: amount norama of the
// signer's earnings to the signer's own bank balance. The chain has no destination field.
func EncodeWithdrawEarnings(signer, amount string) ([]byte, error) {
	if _, err := CanonicalAccount(signer); err != nil {
		return nil, fmt.Errorf("signer: %w", err)
	}
	if err := validateAmount(amount); err != nil {
		return nil, err
	}
	out := appendStringField(nil, 1, signer)
	return appendStringField(out, 2, amount), nil
}
