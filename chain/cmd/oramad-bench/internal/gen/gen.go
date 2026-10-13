// Package gen holds the transaction types the load generator can send. Each type prepares the chain
// state it needs, then plans the k-th transaction of a run as a message; signing and sending are
// the runner's. A type whose preconditions cannot be set up on the chain at hand is skipped with the
// reason, never faked.
package gen

import (
	"context"
	"errors"
	"fmt"
	"sort"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/client/tx"
	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/ops"
	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/shielded"
)

// Signerless is Planned.Account for a transaction nobody signs.
const Signerless = shielded.Signerless

// Planned is one transaction a type wants sent.
type Planned struct {
	// Account indexes Env.Accounts: the signer. Signerless for a transaction with no signature.
	Account int
	Msg     sdk.Msg
	// Gas is the exact gas a signerless transaction declares. Zero means the type's calibrated gas.
	Gas uint64
}

// Env is what a type may use while it prepares and plans.
type Env struct {
	Chain *ops.Chain
	// Accounts are the bench accounts, funded with norama in the bank.
	Accounts []tx.Account
	// Seed is the string the accounts were derived from.
	Seed string
	// RunID is unique to this run. Anything a type creates on the chain that must be unique (a deal
	// nonce, a node id, an asset id) is derived from it, so a second run on the same chain does not
	// collide with the first.
	RunID string
	// Logf reports setup progress.
	Logf func(format string, args ...any)
	// Parallel bounds the setup transactions in flight.
	Parallel int
	// Shielded is the cache of pre-built bundles, nil when the run has none.
	Shielded *shielded.Cache
}

// Log reports progress when a Logf is set.
func (e *Env) Log(format string, args ...any) {
	if e.Logf != nil {
		e.Logf(format, args...)
	}
}

// Generator is one transaction type.
type Generator interface {
	// Name is the type's name in profiles and reports.
	Name() string
	// Describe says in one line what a transaction of the type is.
	Describe() string
	// Prepare sets up the chain state for up to total transactions. It returns a *SkipError when the
	// type cannot run on this chain.
	Prepare(ctx context.Context, env *Env, total int) error
	// Available is how many distinct transactions Plan can produce.
	Available() int
	// Plan returns the k-th transaction, k from 0 to Available()-1. It is deterministic for a given
	// preparation, so the same k is the same message.
	Plan(k int) (Planned, error)
}

// StepAware is implemented by a type whose transactions depend on chain state that moves between
// steps (a tree root a mint names).
type StepAware interface {
	// BeginStep refreshes that state before a step's transactions are planned.
	BeginStep(ctx context.Context, env *Env) error
}

// Consumer is implemented by a type that uses up one-shot material (a pre-proved bundle spends its
// notes once): after a run, the runner marks what it handed out as used.
type Consumer interface {
	// Consumed lists the ids of the cache entries Plan has handed out.
	Consumed() []string
}

// SkipError says a type cannot run, and why. It is a result, not a failure of the run.
type SkipError struct{ Reason string }

func (e *SkipError) Error() string { return "skipped: " + e.Reason }

// Skipf returns a SkipError.
func Skipf(format string, args ...any) error {
	return &SkipError{Reason: fmt.Sprintf(format, args...)}
}

// SkipReason returns the reason when err is, or wraps, a SkipError.
func SkipReason(err error) (string, bool) {
	var skip *SkipError
	if errors.As(err, &skip) {
		return skip.Reason, true
	}
	return "", false
}

// Unlimited is Available() for a type that can plan any number of distinct transactions.
const Unlimited = int(^uint(0) >> 1)

// roundRobin spreads transaction k over n accounts, so consecutive transactions are from different
// accounts and each account's own transactions stay in sequence order.
func roundRobin(k, n int) int { return k % n }

// pair returns the sender and receiver account indexes of transaction k: each account sends to its
// neighbour, never to itself.
func pair(k, n int) (from, to int) {
	from = roundRobin(k, n)
	return from, (from + 1) % n
}

var constructors = map[string]func() Generator{}

func register(name string, ctor func() Generator) { constructors[name] = ctor }

// New returns a fresh generator for a type name.
func New(name string) (Generator, error) {
	ctor, ok := constructors[name]
	if !ok {
		return nil, fmt.Errorf("unknown transaction type %q; known types: %v", name, Names())
	}
	return ctor(), nil
}

// Names lists every transaction type, sorted.
func Names() []string {
	out := make([]string, 0, len(constructors))
	for name := range constructors {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
