//go:build cgo && !nowasm

package app_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
)

// stepChecked finalizes n empty blocks and runs the full conservation pass after each.
func (c *wasmChain) stepChecked(n int) {
	c.t.Helper()
	for i := 0; i < n; i++ {
		c.blocks(1)
		requireConserved(c.t, c.app)
	}
}

func (c *wasmChain) createEscrow(user wasmUser, escrow sdk.AccAddress, id string, arbiter, recipient sdk.AccAddress, funds sdk.Coins) {
	c.t.Helper()
	c.mustExec(user, escrow, map[string]any{"create": map[string]any{
		"id": id, "arbiter": arbiter.String(), "recipient": recipient.String(),
		"title": "t", "description": "d", "end_height": nil, "end_time": nil, "cw20_whitelist": nil,
	}}, funds)
	requireConserved(c.t, c.app)
}

// A contract is an account like any other to the bank, but the conservation pass reads the wasm module
// account's ledgers too (storage deposits), so a contract that holds norama, one that holds a factory
// token, and the instantiation funds a contract is born with are each driven through blocks.
func TestConservation_contractsHoldingNoramaAndTokensAreNotViolations(t *testing.T) {
	forEachCadence(t, func(t *testing.T) {
		c := newWasmChain(t, wasmChainOptions{users: 3})
		alice, bob, carol := c.users[0], c.users[1], c.users[2]

		// Born with funds: the contract holds norama from its first block.
		escrow := c.instantiate(alice, codeEscrow, map[string]any{}, noramaCoins(2*params.NoramaPerOrama))
		requireConserved(t, c.app)
		require.True(t, c.balance(escrow, params.BaseDenom).Equal(math.NewInt(2*params.NoramaPerOrama)))

		// Norama escrowed, then paid out to the recipient, then another refunded to its creator.
		for _, id := range []string{"pay-deal", "refund-deal", "held-deal"} {
			c.createEscrow(alice, escrow, id, bob.addr, carol.addr, noramaCoins(5*params.NoramaPerOrama))
		}
		c.mustExec(bob, escrow, map[string]any{"approve": map[string]string{"id": "pay-deal"}}, nil)
		requireConserved(t, c.app)
		c.mustExec(bob, escrow, map[string]any{"refund": map[string]string{"id": "refund-deal"}}, nil)
		requireConserved(t, c.app)

		// A factory token held by a contract: the pass sums balances per denom and checks x/token's issued supply.
		gold := c.createGold(alice, 1_000)
		requireConserved(t, c.app)
		c.createEscrow(alice, escrow, "gold-deal", bob.addr, carol.addr, sdk.NewCoins(sdk.NewCoin(gold, math.NewInt(100))))
		c.mustExec(bob, escrow, map[string]any{"approve": map[string]string{"id": "gold-deal"}}, nil)
		requireConserved(t, c.app)

		// One deal is still open at the end: the contract still holds its coins while blocks go by.
		require.True(t, c.balance(escrow, params.BaseDenom).GTE(math.NewInt(7*params.NoramaPerOrama)))
		c.stepChecked(10)
	})
}
