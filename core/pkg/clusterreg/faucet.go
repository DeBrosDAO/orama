package clusterreg

import "fmt"

// FaucetTypeURL is the Any type URL of orama.emission.v1.MsgFaucet.
const FaucetTypeURL = "/orama.emission.v1.MsgFaucet"

// Faucet is MsgFaucet: Signer asks the faucet to mint Amount norama to Recipient. Any existing
// account may sign; the signer pays the transaction fee.
type Faucet struct {
	Signer    string
	Recipient string
	// Amount is a positive integer of norama.
	Amount string
}

// ValidateFaucet checks the stateless rules of MsgFaucet.ValidateBasic: both accounts are
// canonical bech32 and the amount is a positive integer. The faucet's limits depend on the
// chain's parameters and are the chain's to check.
func ValidateFaucet(f Faucet) error {
	if _, err := CanonicalAccount(f.Signer); err != nil {
		return fmt.Errorf("signer: %w", err)
	}
	if _, err := CanonicalAccount(f.Recipient); err != nil {
		return fmt.Errorf("recipient: %w", err)
	}
	if !positiveInteger(f.Amount) {
		return fmt.Errorf("amount must be a positive integer of %s", FeeDenom)
	}
	return nil
}

// EncodeFaucet is the protobuf orama.emission.v1.MsgFaucet. Field numbers match
// chain/x/emission/types/tx.pb.go.
func EncodeFaucet(f Faucet) []byte {
	out := appendStringField(nil, 1, f.Signer)
	out = appendStringField(out, 2, f.Recipient)
	return appendStringField(out, 3, f.Amount)
}
