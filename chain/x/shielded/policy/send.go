// Package policy is the shielded-transfer rule set from
// plans/open-network/track-c-chain.md C12 and plans/open-network.md D7.
// It does not verify a proof. Verification is chain/x/shielded/verify.
package policy

import "errors"

const (
	// BaseDenom is the only denom this package treats as mandatory-shielded.
	BaseDenom = "norama"
)

var (
	// ErrPublicPayment is a user-to-user norama send. Those payments are
	// shielded transfers, not bank sends.
	ErrPublicPayment = errors.New("public user-to-user norama transfer is refused")
)

// BlockUserToUser reports whether a bank send of denom from one account to
// another is allowed. Module accounts may still move norama. Two user
// accounts may not.
func BlockUserToUser(fromModule, toModule bool, denom string) error {
	if denom != BaseDenom {
		return nil
	}
	if fromModule || toModule {
		return nil
	}
	return ErrPublicPayment
}
