//go:build cgo && !nowasm

package app_test

import (
	"testing"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	tokentypes "github.com/DeBrosOfficial/network/chain/x/token/types"
)

// A contract that burns a factory token with CosmosMsg::Bank(Burn) destroys supply behind
// x/token's back: Token.Issued stays, bank supply drops, and the per-block conservation pass
// (which halts the chain) fails. The bank burn path must either be refused for factory denoms or
// keep Issued in step.
func TestBindings_bankBurnOfAFactoryTokenKeepsTheLedgerHonest(t *testing.T) {
	b := newBindingChain(t)
	denom := tokentypes.Denom(b.relay.String(), "gold")
	b.bind(b.alice, map[string]any{"token": map[string]any{"create": map[string]any{
		"subdenom": "gold", "name": "Gold", "symbol": "GLD", "description": "d", "mint": true,
	}}})
	b.bind(b.alice, map[string]any{"token": map[string]any{"mint": map[string]any{"denom": denom, "recipient": b.relay.String(), "amount": "100"}}})
	require.True(t, b.balance(b.relay, denom).Equal(math.NewInt(100)))

	burn := map[string]any{"bank": map[string]any{"burn": map[string]any{"amount": []map[string]string{{"denom": denom, "amount": "40"}}}}}
	res := b.exec(b.alice, b.relay, map[string]any{"dispatch": map[string]any{"msgs": []any{burn}}}, nil)
	if res.Code != 0 {
		return // refusing the burn is an acceptable fix
	}
	inv, err := b.app.TokenKeeper.CheckInvariants(b.ctx())
	require.NoError(t, err)
	require.True(t, inv.SupplyMatches, "x/token issued supply drifted from bank supply after a contract's bank burn:\n%s", inv.Detail)
	require.NoError(t, b.app.CheckConservationFull(b.ctx()))
}
