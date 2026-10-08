package indexer

import (
	"encoding/hex"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	abci "github.com/cometbft/cometbft/abci/types"
	cmttypes "github.com/cometbft/cometbft/types"
	gogoproto "github.com/cosmos/gogoproto/proto"

	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/bech32"
	txtypes "github.com/cosmos/cosmos-sdk/types/tx"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
)

type anyT = codectypes.Any

func anyOfPub(t *testing.T, key *secp256k1.PrivKey) *anyT {
	t.Helper()
	a, err := codectypes.NewAnyWithValue(key.PubKey())
	require.NoError(t, err)
	return a
}

// signedSend is a bank send as a wallet builds it: a signer's public key in the
// auth info, a memo, and the "tx" event the fee ante handler emits with the base
// fee it burned.
func signedSend(t *testing.T, key *secp256k1.PrivKey, to, memo, baseFee string) (fakeTx, string) {
	t.Helper()
	from, err := bech32.ConvertAndEncode(params.Bech32Prefix, key.PubKey().Address().Bytes())
	require.NoError(t, err)
	msg := &banktypes.MsgSend{FromAddress: from, ToAddress: to, Amount: sdk.NewCoins(sdk.NewInt64Coin(params.BaseDenom, 5))}
	body := txtypes.TxBody{Memo: memo, Messages: nil}
	body.Messages = append(body.Messages, anyOf(t, msg))
	bodyBytes, err := gogoproto.Marshal(&body)
	require.NoError(t, err)
	auth := txtypes.AuthInfo{SignerInfos: []*txtypes.SignerInfo{{PublicKey: anyOfPub(t, key)}}}
	authBytes, err := gogoproto.Marshal(&auth)
	require.NoError(t, err)
	raw, err := gogoproto.Marshal(&txtypes.TxRaw{BodyBytes: bodyBytes, AuthInfoBytes: authBytes})
	require.NoError(t, err)
	data, err := gogoproto.Marshal(&sdk.TxMsgData{MsgResponses: []*anyT{anyOf(t, &banktypes.MsgSendResponse{})}})
	require.NoError(t, err)
	events := append(addrEvents([]string{from, to}), abci.Event{Type: "tx", Attributes: []abci.EventAttribute{
		{Key: "fee", Value: "120norama"}, {Key: "base_fee", Value: baseFee}, {Key: "tip", Value: "20"},
	}})
	return fakeTx{raw: raw, res: &abci.ExecTxResult{Code: 0, Data: data, GasWanted: 200000, GasUsed: 50000, Events: events}}, from
}

func TestFollower_keepsWhoSignedWhatTheyWroteAndWhatItBurned(t *testing.T) {
	key := secp256k1.GenPrivKeyFromSecret([]byte("explorer"))
	bob := addr(t, 2)
	chain := newFakeChain()
	send, alice := signedSend(t, key, bob, "rent", "100")
	chain.add(send)
	store := index(t, chain, 1)

	got, ok, err := store.Tx(send.raw.Hash())
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, alice, got.Signer)
	require.Equal(t, "rent", got.Memo)
	require.Equal(t, time.Unix(1_700_000_001, 0).UTC(), got.Time)
	require.Len(t, got.Body, 1)
	require.JSONEq(t, fmt.Sprintf(`{"@type":"/cosmos.bank.v1beta1.MsgSend","from_address":%q,"to_address":%q,"amount":[{"denom":"norama","amount":"5"}]}`, alice, bob), string(got.Body[0]))

	blk, ok, err := store.Block(1)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, int64(50000), blk.GasUsed)
	require.Equal(t, "100", blk.Burned)
}

func TestFollower_aMessageOfAnUnknownTypeIsKeptAsItsTypeURL(t *testing.T) {
	chain := newFakeChain()
	body := txtypes.TxBody{Messages: []*anyT{{TypeUrl: "/not.registered.v1.MsgThing", Value: []byte{1, 2, 3}}}}
	bodyBytes, err := gogoproto.Marshal(&body)
	require.NoError(t, err)
	raw, err := gogoproto.Marshal(&txtypes.TxRaw{BodyBytes: bodyBytes})
	require.NoError(t, err)
	data, err := gogoproto.Marshal(&sdk.TxMsgData{MsgResponses: []*anyT{{TypeUrl: "/not.registered.v1.MsgThingResponse"}}})
	require.NoError(t, err)
	chain.add(fakeTx{raw: raw, res: &abci.ExecTxResult{Data: data, Events: addrEvents(nil)}})
	store := index(t, chain, 1)

	got, ok, err := store.Tx(cmttypes.Tx(raw).Hash())
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, []string{"/not.registered.v1.MsgThing"}, got.Messages)
	require.Len(t, got.Body, 1)
	require.JSONEq(t, `{"@type":"/not.registered.v1.MsgThing"}`, string(got.Body[0]))
	require.Empty(t, got.Signer, "a transaction with no signature has no signer")
}

func TestStore_latestTxsAreNewestFirst(t *testing.T) {
	alice, bob := addr(t, 1), addr(t, 2)
	chain := newFakeChain()
	first, second, third := bankSend(t, alice, bob), bankSend(t, bob, alice), bankSend(t, alice, bob)
	chain.add(first, second)
	chain.add(third)
	store := index(t, chain, 1)

	txs, err := store.LatestTxs(2)
	require.NoError(t, err)
	require.Len(t, txs, 2)
	require.Equal(t, hex.EncodeToString(third.raw.Hash()), txs[0].Hash)
	require.Equal(t, hex.EncodeToString(second.raw.Hash()), txs[1].Hash)

	all, err := store.LatestTxs(MaxLimit)
	require.NoError(t, err)
	require.Len(t, all, 3)
}

func TestStore_latestTxsOfAnEmptyIndex(t *testing.T) {
	store := openStore(t, t.TempDir())
	defer store.Close()
	txs, err := store.LatestTxs(10)
	require.NoError(t, err)
	require.Empty(t, txs)
	hours, err := store.Stats()
	require.NoError(t, err)
	require.Empty(t, hours)
}

func TestStore_accountSummaryCountsAndDates(t *testing.T) {
	alice, bob, nobody := addr(t, 1), addr(t, 2), addr(t, 9)
	chain := newFakeChain()
	chain.add(bankSend(t, alice, bob))
	chain.add()
	chain.add(bankSend(t, bob, alice), bankSend(t, alice, bob))
	store := index(t, chain, 1)

	sum, ok, err := store.Account(alice)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, uint64(3), sum.TxCount)
	require.Equal(t, time.Unix(1_700_000_001, 0).UTC(), sum.FirstSeen)
	require.Equal(t, time.Unix(1_700_000_003, 0).UTC(), sum.LastActive)

	_, ok, err = store.Account(nobody)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestStore_statsBucketHoursWithoutHoles(t *testing.T) {
	alice, bob := addr(t, 1), addr(t, 2)
	chain := newFakeChain()
	chain.add(bankSend(t, alice, bob), failedTx(t, []gogoproto.Message{&banktypes.MsgSend{FromAddress: alice, ToAddress: bob}}, alice))
	chain.add(bankSend(t, bob, alice))
	store := index(t, chain, 1)

	hours, err := store.Stats()
	require.NoError(t, err)
	require.Len(t, hours, HourlyWindow)
	last := hours[len(hours)-1]
	require.Equal(t, hourOf(time.Unix(1_700_000_002, 0)), last.Hour.Unix()/3600)
	require.Equal(t, uint64(3), last.Txs)
	require.Equal(t, uint64(1), last.Failed)
	require.Equal(t, "0", last.Burned)
	require.Zero(t, hours[0].Txs, "an hour with no transaction is a bucket of zeros")
	for i := 1; i < len(hours); i++ {
		require.Equal(t, time.Hour, hours[i].Hour.Sub(hours[i-1].Hour))
	}
}

func TestStore_refusesAnIndexOfAnotherLayout(t *testing.T) {
	dir := t.TempDir()
	store := openStore(t, dir)
	_, err := NewFollower(newFakeChain(), store, 1)
	require.NoError(t, err)
	require.NoError(t, store.db.Set([]byte(keyVersion), be64(schemaVersion-1), nil))
	require.NoError(t, store.Close())

	_, err = Open(dir)
	require.ErrorContains(t, err, "index again into a new --home")
}

func TestStore_refusesAnIndexWrittenBeforeLayoutVersions(t *testing.T) {
	dir := t.TempDir()
	store := openStore(t, dir)
	_, err := NewFollower(newFakeChain(), store, 1)
	require.NoError(t, err)
	require.NoError(t, store.db.Delete([]byte(keyVersion), nil))
	require.NoError(t, store.Close())

	_, err = Open(dir)
	require.ErrorContains(t, err, "written before layout version")
}

func TestAPI_servesLatestTxsStatsAndAccountSummary(t *testing.T) {
	api, _, alice, _ := apiFixture(t)

	code, body := get(t, api, http.MethodGet, "/index/v1/txs?limit=2")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, body["txs"], 2)
	require.Equal(t, 3.0, body["txs"].([]any)[0].(map[string]any)["height"])
	code, body = get(t, api, http.MethodGet, "/index/v1/txs")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, body["txs"], 4, "default limit covers all four")

	code, body = get(t, api, http.MethodGet, "/index/v1/stats")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, body["hours"], HourlyWindow)

	code, body = get(t, api, http.MethodGet, "/index/v1/accounts/"+alice)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, alice, body["address"])
	require.Equal(t, 4.0, body["tx_count"])

	code, body = get(t, api, http.MethodGet, "/index/v1/accounts/"+addr(t, 9))
	require.Equal(t, http.StatusNotFound, code)
	require.Equal(t, "not indexed", body["error"])
}

func TestAPI_refusesBadExplorerQueries(t *testing.T) {
	api, _, alice, _ := apiFixture(t)
	for _, target := range []string{
		"/index/v1/txs?limit=0",
		"/index/v1/txs?limit=101",
		"/index/v1/txs?limit=1&limit=2",
		"/index/v1/txs?page=2",
		"/index/v1/txs?limit=",
		"/index/v1/stats?hours=3",
		"/index/v1/accounts/" + alice + "?x=1",
		"/index/v1/accounts/" + "ORAMA1" + alice[6:],
		"/index/v1/accounts/notanaddress",
	} {
		code, _ := get(t, api, http.MethodGet, target)
		require.Equal(t, http.StatusBadRequest, code, target)
	}
}
