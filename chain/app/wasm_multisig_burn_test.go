//go:build cgo && !nowasm

package app_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
)

// The standard multisig executes any CosmosMsg a passed proposal carries, as itself. Instantiating
// it is open to everybody before the upload sunset, so anybody can reach CosmosMsg::Bank(Burn) with
// no contract of their own. Burning a factory token that way lowers its bank supply without
// lowering x/token's issued supply, which the per-block conservation pass treats as a halt.
func TestStandardMultisig_cannotDesyncAFactoryTokenSupplyWithABankBurn(t *testing.T) {
	c := newWasmChain(t, wasmChainOptions{users: 2})
	alice := c.users[0]
	denom := c.createGold(alice, 1_000)

	multisig := c.instantiate(alice, codeMultisig, map[string]any{
		"voters":            []map[string]any{{"addr": alice.addr.String(), "weight": 1}},
		"threshold":         map[string]any{"absolute_count": map[string]any{"weight": 1}},
		"max_voting_period": map[string]any{"height": 1000},
	}, nil)
	// Alice pays her own token to the multisig with an ordinary bank send.
	c.mustDeliver(alice, &banktypes.MsgSend{FromAddress: alice.addr.String(), ToAddress: multisig.String(), Amount: sdk.NewCoins(sdk.NewInt64Coin(denom, 400))})
	require.True(t, c.balance(multisig, denom).Equal(math.NewInt(400)))

	burn := map[string]any{"bank": map[string]any{"burn": map[string]any{"amount": []map[string]string{{"denom": denom, "amount": "400"}}}}}
	c.mustExec(alice, multisig, map[string]any{"propose": map[string]any{"title": "burn", "description": "x", "msgs": []any{burn}}}, nil)
	res := c.exec(alice, multisig, map[string]any{"execute": map[string]any{"proposal_id": 1}}, nil)
	if res.Code != 0 {
		return // refusing the burn is an acceptable fix
	}
	inv, err := c.app.TokenKeeper.CheckInvariants(c.ctx())
	require.NoError(t, err)
	require.True(t, inv.SupplyMatches, "x/token issued supply drifted from the bank supply:\n%s", inv.Detail)
}
