package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	"github.com/DeBrosOfficial/network/chain/x/token/types"
)

// MsgTransfer sends with the keeper-level bank.SendCoins, which skips the blocked-address list that
// x/bank's MsgSend enforces; the keeper has to refuse a module account as the receiver itself.
func TestTransfer_refusesABlockedReceiver(t *testing.T) {
	holder, blocked := addr(2), addr(5)
	f := newTestFixture(t, nil)
	f.initGenesis(t, nil)
	token := f.create(t, addr(1), "plain", func(m *types.MsgCreateToken) { m.Mint = true })
	f.mint(t, addr(1), token.Denom, holder, 1_000)
	f.Bank.blocked[blocked.String()] = true

	err := f.Keeper.Transfer(f.Ctx, transferMsg(holder, blocked, token.Denom, 500))
	require.ErrorContains(t, err, "module account")
	require.True(t, f.Bank.balanceOf(blocked.String(), token.Denom).IsZero())
	require.True(t, f.Bank.balanceOf(holder.String(), token.Denom).Equal(math.NewInt(1_000)))
}

func TestTransfer_refusesTheTokenModuleAccountAsReceiver(t *testing.T) {
	holder := addr(2)
	f := newTestFixture(t, nil)
	f.initGenesis(t, nil)
	token := f.create(t, addr(1), "plain", func(m *types.MsgCreateToken) { m.Mint = true })
	f.mint(t, addr(1), token.Denom, holder, 1_000)

	err := f.Keeper.Transfer(f.Ctx, transferMsg(holder, authtypes.NewModuleAddress(types.ModuleName), token.Denom, 500))
	require.ErrorContains(t, err, "module account")
	require.True(t, f.Bank.balanceOf(holder.String(), token.Denom).Equal(math.NewInt(1_000)))
}

// An ordinary receiver is unaffected.
func TestTransfer_allowsAnOrdinaryReceiver(t *testing.T) {
	holder, other := addr(2), addr(3)
	f := newTestFixture(t, nil)
	f.initGenesis(t, nil)
	token := f.create(t, addr(1), "plain", func(m *types.MsgCreateToken) { m.Mint = true })
	f.mint(t, addr(1), token.Denom, holder, 1_000)

	require.NoError(t, f.Keeper.Transfer(f.Ctx, transferMsg(holder, other, token.Denom, 500)))
	require.True(t, f.Bank.balanceOf(other.String(), token.Denom).Equal(math.NewInt(500)))
}
