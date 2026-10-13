//go:build cgo && !nowasm

package app_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A contract once kept at most 32 deposit payers. 31 throwaway accounts that call
// the standard CW20's increase_allowance (which needs no balance) take the remaining slots, after
// which no other account can grow the token's state: the next holder who pays a new recipient is
// refused. The slots must not be a resource a stranger can exhaust for a
// contract he does not use.
func TestDeposit_strangersCannotFillTheChunkSlotsOfAContract(t *testing.T) {
	const sybils = 31
	c := newWasmChain(t, wasmChainOptions{users: 3 + sybils})
	alice, bob, carol := c.users[0], c.users[1], c.users[2]
	token := c.instantiate(alice, codeCW20, map[string]any{
		"name": "Gold", "symbol": "GLD", "decimals": 6,
		"initial_balances": []map[string]string{{"address": alice.addr.String(), "amount": "1000"}},
	}, nil)
	c.mustExec(alice, token, map[string]any{"transfer": map[string]string{"recipient": bob.addr.String(), "amount": "250"}}, nil)

	for _, s := range c.users[3:] {
		c.mustExec(s, token, map[string]any{"increase_allowance": map[string]any{"spender": carol.addr.String(), "amount": "1"}}, nil)
	}

	res := c.exec(bob, token, map[string]any{"transfer": map[string]string{"recipient": carol.addr.String(), "amount": "10"}}, nil)
	require.Zero(t, res.Code, "an honest holder was locked out of the token by strangers' deposit chunks: %s", res.Log)
}
