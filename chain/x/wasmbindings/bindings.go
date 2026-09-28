// Package wasmbindings holds the Orama contract bindings for modules that are not
// in this binary yet. The default implementation of every method returns NOT_LINKED.
// Nothing here imports x/token, x/cnft, x/market, x/storage, or x/shielded.
package wasmbindings

import "cosmossdk.io/errors"

const (
	// ModuleName is the codespace for binding errors.
	ModuleName = "wasmbindings"

	// NotLinkedCode is the coded error text. Tests match this string.
	NotLinkedCode = "NOT_LINKED"
)

// ErrNotLinked is the coded NOT_LINKED error.
var ErrNotLinked = errors.Register(ModuleName, 1, NotLinkedCode)

// Token is the x/token binding: create, mint, and burn for tokens the contract administers.
type Token interface {
	Create(contract, subdenom string) error
	Mint(contract, subdenom, to, amount string) error
	Burn(contract, subdenom, from, amount string) error
}

// CNFT is the x/cnft binding: mint into a tree the contract owns, and verify a proof.
type CNFT interface {
	Mint(contract, tree, owner string) error
	VerifyProof(tree string, proof, leaf []byte) error
}

// Market is the x/market binding.
type Market interface {
	List(contract, listing string) error
	Bid(contract, listing string) error
	Settle(contract, listing string) error
}

// Storage is the x/storage binding: create a deal from the contract's own funds.
type Storage interface {
	CreateDeal(contract, cid string) error
}

// Shielded is the x/shielded binding. Contracts never hold notes; they shield
// from their balance and receive unshield outputs.
type Shielded interface {
	Shield(contract, amount string) error
	ReceiveUnshield(contract, amount string) error
}

// TokenBinding is the default x/token binding.
type TokenBinding struct{}

// CNFTBinding is the default x/cnft binding.
type CNFTBinding struct{}

// MarketBinding is the default x/market binding.
type MarketBinding struct{}

// StorageBinding is the default x/storage binding.
type StorageBinding struct{}

// ShieldedBinding is the default x/shielded binding.
type ShieldedBinding struct{}

// Default groups the unwired bindings.
type Default struct {
	Token    TokenBinding
	CNFT     CNFTBinding
	Market   MarketBinding
	Storage  StorageBinding
	Shielded ShieldedBinding
}

var (
	_ Token    = TokenBinding{}
	_ CNFT     = CNFTBinding{}
	_ Market   = MarketBinding{}
	_ Storage  = StorageBinding{}
	_ Shielded = ShieldedBinding{}
)

// Create returns NOT_LINKED.
func (TokenBinding) Create(string, string) error { return ErrNotLinked }

// Mint returns NOT_LINKED.
func (TokenBinding) Mint(string, string, string, string) error { return ErrNotLinked }

// Burn returns NOT_LINKED.
func (TokenBinding) Burn(string, string, string, string) error { return ErrNotLinked }

// Mint returns NOT_LINKED.
func (CNFTBinding) Mint(string, string, string) error { return ErrNotLinked }

// VerifyProof returns NOT_LINKED.
func (CNFTBinding) VerifyProof(string, []byte, []byte) error { return ErrNotLinked }

// List returns NOT_LINKED.
func (MarketBinding) List(string, string) error { return ErrNotLinked }

// Bid returns NOT_LINKED.
func (MarketBinding) Bid(string, string) error { return ErrNotLinked }

// Settle returns NOT_LINKED.
func (MarketBinding) Settle(string, string) error { return ErrNotLinked }

// CreateDeal returns NOT_LINKED.
func (StorageBinding) CreateDeal(string, string) error { return ErrNotLinked }

// Shield returns NOT_LINKED.
func (ShieldedBinding) Shield(string, string) error { return ErrNotLinked }

// ReceiveUnshield returns NOT_LINKED.
func (ShieldedBinding) ReceiveUnshield(string, string) error { return ErrNotLinked }
