package wasmbindings_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	abci "github.com/cometbft/cometbft/abci/types"

	wasmvmtypes "github.com/CosmWasm/wasmvm/v3/types"

	"github.com/cosmos/cosmos-sdk/baseapp"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"

	tokentypes "github.com/DeBrosOfficial/network/chain/x/token/types"
	"github.com/DeBrosOfficial/network/chain/x/wasmbindings"
)

type recordingNext struct{ calls int }

func (r *recordingNext) DispatchMsg(sdk.Context, sdk.AccAddress, string, wasmvmtypes.CosmosMsg) ([]sdk.Event, [][]byte, [][]*codectypes.Any, error) {
	r.calls++
	return nil, nil, nil, nil
}

type fakeRouter struct {
	seen []sdk.Msg
	fail error
}

func (f *fakeRouter) Handler(msg sdk.Msg) baseapp.MsgServiceHandler {
	return func(_ sdk.Context, m sdk.Msg) (*sdk.Result, error) {
		f.seen = append(f.seen, m)
		if f.fail != nil {
			return nil, f.fail
		}
		return &sdk.Result{Data: []byte("ok"), Events: []abci.Event{{Type: "routed"}}}, nil
	}
}

type fakeEarnings struct {
	payer, recipient sdk.AccAddress
	amount           sdk.Coin
	fail             error
}

func (f *fakeEarnings) PayEarnings(_ context.Context, payer, recipient sdk.AccAddress, amt sdk.Coin) error {
	f.payer, f.recipient, f.amount = payer, recipient, amt
	return f.fail
}

func msgCtx(t *testing.T) sdk.Context {
	t.Helper()
	key := storetypes.NewKVStoreKey("msgs")
	return testutil.DefaultContextWithDB(t, key, storetypes.NewTransientStoreKey("t_msgs")).Ctx
}

func custom(raw string) wasmvmtypes.CosmosMsg {
	return wasmvmtypes.CosmosMsg{Custom: json.RawMessage(raw)}
}

func TestMessenger_routesACustomMessageAsTheContract(t *testing.T) {
	next, router, earnings := &recordingNext{}, &fakeRouter{}, &fakeEarnings{}
	m := wasmbindings.NewMessenger(next, router, earnings)

	events, data, responses, err := m.DispatchMsg(msgCtx(t), contract, "", custom(`{"token":{"burn":{"denom":"factory/x/gold","amount":"2"}}}`))
	require.NoError(t, err)
	require.Zero(t, next.calls, "a custom message never reaches wasmd's own handler")
	require.Len(t, router.seen, 1)
	require.Equal(t, contract.String(), router.seen[0].(*tokentypes.MsgBurn).Sender)
	require.Len(t, events, 1)
	require.Equal(t, [][]byte{[]byte("ok")}, data)
	require.Len(t, responses, 1)
}

func TestMessenger_errorsFromTheModuleSurface(t *testing.T) {
	router := &fakeRouter{fail: fmt.Errorf("mint authority for x is not held")}
	m := wasmbindings.NewMessenger(&recordingNext{}, router, &fakeEarnings{})
	_, _, _, err := m.DispatchMsg(msgCtx(t), contract, "", custom(`{"token":{"burn":{"denom":"factory/x/gold","amount":"2"}}}`))
	require.ErrorContains(t, err, "mint authority")
}

func TestMessenger_failsValidateBasicBeforeRouting(t *testing.T) {
	router := &fakeRouter{}
	m := wasmbindings.NewMessenger(&recordingNext{}, router, &fakeEarnings{})
	_, _, _, err := m.DispatchMsg(msgCtx(t), contract, "", custom(`{"storage":{"create_deal":{"class":1}}}`))
	require.Error(t, err)
	require.Empty(t, router.seen)
}

func TestMessenger_passesEveryOtherMessageThrough(t *testing.T) {
	next := &recordingNext{}
	m := wasmbindings.NewMessenger(next, &fakeRouter{}, &fakeEarnings{})
	_, _, _, err := m.DispatchMsg(msgCtx(t), contract, "", wasmvmtypes.CosmosMsg{Bank: &wasmvmtypes.BankMsg{Burn: &wasmvmtypes.BurnMsg{}}})
	require.NoError(t, err)
	require.Equal(t, 1, next.calls)
}

func TestMessenger_refusesAnyStargateAndSetWithdrawAddress(t *testing.T) {
	next := &recordingNext{}
	m := wasmbindings.NewMessenger(next, &fakeRouter{}, &fakeEarnings{})
	for name, msg := range map[string]wasmvmtypes.CosmosMsg{
		"any":                  {Any: &wasmvmtypes.AnyMsg{TypeURL: "/cosmos.bank.v1beta1.MsgSend"}},
		"set withdraw address": {Distribution: &wasmvmtypes.DistributionMsg{SetWithdrawAddress: &wasmvmtypes.SetWithdrawAddressMsg{Address: user.String()}}},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, _, err := m.DispatchMsg(msgCtx(t), contract, "", msg)
			require.ErrorIs(t, err, wasmbindings.ErrDisabledMessage)
		})
	}
	require.Zero(t, next.calls)

	// The rest of the distribution variants are left to wasmd.
	_, _, _, err := m.DispatchMsg(msgCtx(t), contract, "", wasmvmtypes.CosmosMsg{Distribution: &wasmvmtypes.DistributionMsg{FundCommunityPool: &wasmvmtypes.FundCommunityPoolMsg{}}})
	require.NoError(t, err)
}

func TestMessenger_earningsPaymentMovesNoramaFromTheContract(t *testing.T) {
	earnings := &fakeEarnings{}
	m := wasmbindings.NewMessenger(&recordingNext{}, &fakeRouter{}, earnings)
	events, _, _, err := m.DispatchMsg(msgCtx(t), contract, "", custom(`{"earnings":{"pay":{"recipient":"`+user.String()+`","amount":"40"}}}`))
	require.NoError(t, err)
	require.Equal(t, contract, earnings.payer)
	require.Equal(t, user, earnings.recipient)
	require.Equal(t, "40norama", earnings.amount.String())
	require.Len(t, events, 1)

	for name, raw := range map[string]string{
		"bad recipient": `{"earnings":{"pay":{"recipient":"nobody","amount":"1"}}}`,
		"zero amount":   `{"earnings":{"pay":{"recipient":"` + user.String() + `","amount":"0"}}}`,
		"negative":      `{"earnings":{"pay":{"recipient":"` + user.String() + `","amount":"-3"}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, _, _, err := m.DispatchMsg(msgCtx(t), contract, "", custom(raw))
			require.ErrorIs(t, err, wasmbindings.ErrBadMessage)
		})
	}

	earnings.fail = fmt.Errorf("insufficient funds")
	_, _, _, err = m.DispatchMsg(msgCtx(t), contract, "", custom(`{"earnings":{"pay":{"recipient":"`+user.String()+`","amount":"40"}}}`))
	require.ErrorContains(t, err, "insufficient funds")
}

func TestMessenger_shieldedIsNotLinked(t *testing.T) {
	m := wasmbindings.NewMessenger(&recordingNext{}, &fakeRouter{}, &fakeEarnings{})
	_, _, _, err := m.DispatchMsg(msgCtx(t), contract, "", custom(`{"shielded":{"shield":{}}}`))
	require.ErrorIs(t, err, wasmbindings.ErrNotLinked)
}
