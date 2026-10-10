package types

import (
	"context"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

type budgetKey struct{}

// DepositBudget accumulates the state deposit one transaction has locked so far. The ante chain puts
// one in the context; it is a pointer so every message and submessage of the transaction adds to the
// same total, and a submessage that later reverts still counts, which only makes the cap stricter.
type DepositBudget struct {
	Locked math.Int
}

// NewDepositBudget returns an empty budget.
func NewDepositBudget() *DepositBudget { return &DepositBudget{Locked: math.ZeroInt()} }

// WithDepositBudget returns ctx carrying b.
func WithDepositBudget(ctx sdk.Context, b *DepositBudget) sdk.Context {
	return ctx.WithValue(budgetKey{}, b)
}

// DepositBudgetFrom returns the budget WithDepositBudget set, or nil when the call is not part of a
// transaction (genesis, block hooks, direct keeper calls).
func DepositBudgetFrom(ctx context.Context) *DepositBudget {
	b, _ := ctx.Value(budgetKey{}).(*DepositBudget)
	return b
}
