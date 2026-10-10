package types

import (
	"errors"
	"fmt"
)

// ConsensusService is the service name of the binding that holds a validator's consensus key. The
// key is ed25519, and it signs the binding for the node's operator like any other service key, so
// the operator has proved it controls the key CometBFT signs with. x/power attributes a validator
// to the operator of the live node that binds its consensus key, and caps voting power per
// operator.
const ConsensusService = "consensus"

// ErrConsensusKey is returned when a node's consensus binding is not an ed25519 key.
var ErrConsensusKey = errors.New("invalid consensus key binding")

// CheckConsensusBinding verifies that a consensus binding, when there is one, is an ed25519 key.
// It checks shape only; the signature is checked with every other binding, and ValidateBasic
// refuses two bindings for one service. A node with no consensus binding is not a validator's node.
func CheckConsensusBinding(bindings []Binding) error {
	for _, binding := range bindings {
		if binding.Service == ConsensusService && binding.KeyType != KeyTypeEd25519 {
			return fmt.Errorf("the %q binding must be an ed25519 key, got %s: %w", ConsensusService, binding.KeyType, ErrConsensusKey)
		}
	}
	return nil
}
