package keeper_test

import (
	"context"
	"testing"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	"github.com/DeBrosOfficial/network/chain/x/nodes/types"
)

type fakeAccounts struct {
	accounts map[string]sdk.AccountI
	next     uint64
}

func newFakeAccounts() *fakeAccounts { return &fakeAccounts{accounts: map[string]sdk.AccountI{}} }

func (a *fakeAccounts) HasAccount(_ context.Context, addr sdk.AccAddress) bool {
	_, ok := a.accounts[addr.String()]
	return ok
}

func (a *fakeAccounts) NewAccountWithAddress(_ context.Context, addr sdk.AccAddress) sdk.AccountI {
	a.next++
	return authtypes.NewBaseAccount(addr, nil, a.next, 0)
}

func (a *fakeAccounts) SetAccount(_ context.Context, acc sdk.AccountI) {
	a.accounts[acc.GetAddress().String()] = acc
}

// On stagenet every provider's accept failed with "account ... does not exist on chain": the hot
// key had a fee balance but no account, and an address with no account cannot sign.
func TestFundHotKey_givesTheHotKeyAnAccountItCanSignWith(t *testing.T) {
	f := newTestFixture(t)
	op, hot := storageNode(t, f, "node-1", []string{"https://93.184.113.10:443"}, 64495)
	f.Earnings.balances[op.String()] = math.NewInt(1_000)
	require.False(t, f.Accounts.HasAccount(f.Ctx, hot))

	_, err := f.Msg.FundHotKey(f.Ctx, &types.MsgFundHotKey{Operator: op.String(), NodeId: "node-1", Amount: math.NewInt(400)})
	require.NoError(t, err)
	require.True(t, f.Accounts.HasAccount(f.Ctx, hot))
}

// A second funding leaves the account, and so its sequence, as it is.
func TestFundHotKey_keepsAnExistingAccount(t *testing.T) {
	f := newTestFixture(t)
	op, hot := storageNode(t, f, "node-1", []string{"https://93.184.113.10:443"}, 64495)
	f.Earnings.balances[op.String()] = math.NewInt(1_000)
	existing := authtypes.NewBaseAccount(hot, nil, 77, 5)
	f.Accounts.SetAccount(f.Ctx, existing)

	_, err := f.Msg.FundHotKey(f.Ctx, &types.MsgFundHotKey{Operator: op.String(), NodeId: "node-1", Amount: math.NewInt(400)})
	require.NoError(t, err)
	got := f.Accounts.accounts[hot.String()]
	require.Equal(t, uint64(77), got.GetAccountNumber())
	require.Equal(t, uint64(5), got.GetSequence())
}

// A refused funding creates nothing.
func TestFundHotKey_aRefusedFundingCreatesNoAccount(t *testing.T) {
	f := newTestFixture(t)
	op, hot := storageNode(t, f, "node-1", []string{"https://93.184.113.10:443"}, 64495)
	f.Earnings.balances[op.String()] = math.NewInt(10)

	_, err := f.Msg.FundHotKey(f.Ctx, &types.MsgFundHotKey{Operator: op.String(), NodeId: "node-1", Amount: math.NewInt(400)})
	require.Error(t, err)
	require.False(t, f.Accounts.HasAccount(f.Ctx, hot))
}
