// Package wasmbindings is how a CosmWasm contract reaches Orama's modules. A contract sends a
// CosmosMsg::Custom (a JSON object, see msg.go) and queries with QueryRequest::Custom (query.go).
// The contract's own address is always the signer: no message names a sender, so a contract can act
// only for itself. Anything else a contract could reach a module through, CosmosMsg::Any and its
// stargate form, is refused (messenger.go).
//
// x/token, x/cnft, x/market and x/storage are linked. x/shielded is not: its adapter is the
// audited "unshield, call, reshield" flow that plans/open-network/track-c-chain.md C12 owns, and
// every shielded message returns NOT_LINKED until that adapter exists.
package wasmbindings

import "cosmossdk.io/errors"

const (
	// ModuleName is the codespace for binding errors.
	ModuleName = "wasmbindings"

	// NotLinkedCode is the coded error text. Tests match this string.
	NotLinkedCode = "NOT_LINKED"
)

var (
	// ErrNotLinked is the coded NOT_LINKED error.
	ErrNotLinked = errors.Register(ModuleName, 1, NotLinkedCode)

	// ErrBadMessage is returned for a custom message or query that is not valid JSON of exactly one
	// known variant.
	ErrBadMessage = errors.Register(ModuleName, 2, "invalid orama binding message")

	// ErrDisabledMessage is returned for a CosmosMsg variant this chain does not let a contract send.
	ErrDisabledMessage = errors.Register(ModuleName, 3, "this cosmos message variant is disabled for contracts")
)

// Shielded is the x/shielded binding. Contracts never hold notes; they shield
// from their balance and receive unshield outputs. It is not linked.
type Shielded interface {
	Shield(contract, amount string) error
	ReceiveUnshield(contract, amount string) error
}

// ShieldedBinding is the default x/shielded binding.
type ShieldedBinding struct{}

var _ Shielded = ShieldedBinding{}

// Shield returns NOT_LINKED.
func (ShieldedBinding) Shield(string, string) error { return ErrNotLinked }

// ReceiveUnshield returns NOT_LINKED.
func (ShieldedBinding) ReceiveUnshield(string, string) error { return ErrNotLinked }
