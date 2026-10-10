package indexer

import (
	"context"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	abci "github.com/cometbft/cometbft/abci/types"
	cmttypes "github.com/cometbft/cometbft/types"
	gogoproto "github.com/cosmos/gogoproto/proto"

	"github.com/cosmos/cosmos-sdk/types/bech32"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/x/inclusion"
)

func bankSend(t *testing.T, from, to string) fakeTx {
	t.Helper()
	msg := &banktypes.MsgSend{FromAddress: from, ToAddress: to}
	return okTx(t, []gogoproto.Message{msg}, []gogoproto.Message{&banktypes.MsgSendResponse{}}, from, to)
}

func TestFollower_indexesBlocksTransactionsAndAccounts(t *testing.T) {
	ctx := context.Background()
	alice, bob, carol := addr(t, 1), addr(t, 2), addr(t, 3)
	chain := newFakeChain()
	send1 := bankSend(t, alice, bob)
	fail := failedTx(t, []gogoproto.Message{&banktypes.MsgSend{FromAddress: alice, ToAddress: carol}}, alice)
	chain.add(send1, fail)
	send2 := bankSend(t, bob, alice)
	chain.add(send2)
	chain.add()

	store := openStore(t, t.TempDir())
	defer store.Close()
	f, err := NewFollower(chain, store, 1)
	require.NoError(t, err)
	n, err := f.Step(ctx)
	require.NoError(t, err)
	require.Equal(t, 3, n)

	b, ok, err := store.Block(1)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, 2, b.TxCount)
	require.Equal(t, hex.EncodeToString(send1.raw.Hash()), b.TxHashes[0])
	require.Len(t, b.Hash, 64)
	empty, ok, err := store.Block(3)
	require.NoError(t, err)
	require.True(t, ok)
	require.Zero(t, empty.TxCount)
	require.NotNil(t, empty.TxHashes)

	got, ok, err := store.Tx(fail.raw.Hash())
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, uint32(5), got.Code)
	require.Equal(t, "insufficient funds", got.Log)
	require.Equal(t, []string{"/cosmos.bank.v1beta1.MsgSend"}, got.Messages)
	require.Equal(t, uint32(1), got.Index)

	txs, err := store.AccountTxs(alice, 1, 10)
	require.NoError(t, err)
	require.Len(t, txs, 3, "alice is named by both sends and by her failed send")
	require.Equal(t, int64(2), txs[0].Height, "newest first")
	require.Equal(t, fail.raw.Hash(), mustHex(t, txs[1].Hash))

	page2, err := store.AccountTxs(alice, 2, 2)
	require.NoError(t, err)
	require.Len(t, page2, 1)
	require.Equal(t, hex.EncodeToString(send1.raw.Hash()), page2[0].Hash)

	none, err := store.AccountTxs(carol, 1, 10)
	require.NoError(t, err)
	require.Empty(t, none, "an address only in a message body is not in the event index")

	st, err := store.Status()
	require.NoError(t, err)
	require.Equal(t, Status{StartHeight: 1, Cursor: 3}, st)
}

func TestFollower_resumesAfterRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	alice, bob := addr(t, 1), addr(t, 2)
	chain := newFakeChain()
	chain.add(bankSend(t, alice, bob))
	chain.add(bankSend(t, alice, bob))

	store := openStore(t, dir)
	f, err := NewFollower(chain, store, 1)
	require.NoError(t, err)
	_, err = f.Step(ctx)
	require.NoError(t, err)
	require.NoError(t, store.Close())

	chain.add(bankSend(t, bob, alice))
	chain.fetched = nil
	store = openStore(t, dir)
	defer store.Close()
	f, err = NewFollower(chain, store, 1)
	require.NoError(t, err)
	n, err := f.Step(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, []int64{3}, chain.fetched, "a restart resumes after the cursor")

	txs, err := store.AccountTxs(alice, 1, 10)
	require.NoError(t, err)
	require.Len(t, txs, 3, "nothing indexed twice")

	n, err = f.Step(ctx)
	require.NoError(t, err)
	require.Zero(t, n, "caught up")
}

func TestFollower_refusesAPrunedStart(t *testing.T) {
	ctx := context.Background()
	chain := newFakeChain()
	for range 60 {
		chain.add()
	}
	chain.earliest = 50

	store := openStore(t, t.TempDir())
	defer store.Close()
	f, err := NewFollower(chain, store, 10)
	require.NoError(t, err)
	n, err := f.Step(ctx)
	require.ErrorIs(t, err, ErrPruned)
	require.ErrorContains(t, err, "needs block 10")
	require.ErrorContains(t, err, "earliest block is 50")
	require.Zero(t, n)
	require.Empty(t, chain.fetched, "nothing was skipped to")
	st, err := store.Status()
	require.NoError(t, err)
	require.Zero(t, st.Cursor)
}

func TestFollower_refusesWhenTheNodePrunedPastTheCursor(t *testing.T) {
	ctx := context.Background()
	chain := newFakeChain()
	for range 5 {
		chain.add()
	}
	store := openStore(t, t.TempDir())
	defer store.Close()
	f, err := NewFollower(chain, store, 1)
	require.NoError(t, err)
	_, err = f.Step(ctx)
	require.NoError(t, err)

	for range 10 {
		chain.add()
	}
	chain.earliest = 9
	_, err = f.Step(ctx)
	require.ErrorIs(t, err, ErrPruned)
	require.ErrorContains(t, err, "needs block 6")
}

func TestNewFollower_refusesAnotherStartHeightOrZero(t *testing.T) {
	store := openStore(t, t.TempDir())
	defer store.Close()
	_, err := NewFollower(newFakeChain(), store, 0)
	require.Error(t, err)
	_, err = NewFollower(newFakeChain(), store, 7)
	require.NoError(t, err)
	_, err = NewFollower(newFakeChain(), store, 7)
	require.NoError(t, err, "the same start height reopens")
	_, err = NewFollower(newFakeChain(), store, 1)
	require.ErrorContains(t, err, "started from height 7")
}

func TestFollower_stepIsBounded(t *testing.T) {
	chain := newFakeChain()
	for range MaxBlocksPerStep + 3 {
		chain.add()
	}
	store := openStore(t, t.TempDir())
	defer store.Close()
	f, err := NewFollower(chain, store, 1)
	require.NoError(t, err)
	n, err := f.Step(context.Background())
	require.NoError(t, err)
	require.Equal(t, MaxBlocksPerStep, n)
	n, err = f.Step(context.Background())
	require.NoError(t, err)
	require.Equal(t, 3, n)
}

func TestFollower_refusesInconsistentBlocksAndKeepsTheCursor(t *testing.T) {
	ctx := context.Background()
	alice, bob := addr(t, 1), addr(t, 2)

	mismatch := newFakeChain()
	mismatch.add(bankSend(t, alice, bob))
	mismatch.blocks[0].results = nil
	store := openStore(t, t.TempDir())
	defer store.Close()
	f, err := NewFollower(mismatch, store, 1)
	require.NoError(t, err)
	_, err = f.Step(ctx)
	require.ErrorContains(t, err, "1 transactions but 0 results")

	garbage := newFakeChain()
	garbage.add(fakeTx{raw: cmttypes.Tx("not a tx"), res: &abci.ExecTxResult{Code: 0}})
	store2 := openStore(t, t.TempDir())
	defer store2.Close()
	f2, err := NewFollower(garbage, store2, 1)
	require.NoError(t, err)
	_, err = f2.Step(ctx)
	require.ErrorContains(t, err, "successful transaction")
	st, err := store2.Status()
	require.NoError(t, err)
	require.Zero(t, st.Cursor, "a block that failed to index is not half-committed")
	_, ok, err := store2.Block(1)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestFollower_indexesARefusedUndecodableTransaction(t *testing.T) {
	chain := newFakeChain()
	junk := cmttypes.Tx("not a tx")
	chain.add(fakeTx{raw: junk, res: &abci.ExecTxResult{Code: 2, Codespace: "sdk", Log: "tx parse error"}})
	store := openStore(t, t.TempDir())
	defer store.Close()
	f, err := NewFollower(chain, store, 1)
	require.NoError(t, err)
	_, err = f.Step(context.Background())
	require.NoError(t, err)
	got, ok, err := store.Tx(junk.Hash())
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, uint32(2), got.Code)
	require.Empty(t, got.Messages)
}

func TestEventAddresses_takesOnlyWholeAccountAddresses(t *testing.T) {
	alice := addr(t, 1)
	valoper, err := bech32.ConvertAndEncode(params.Bech32PrefixValAddr, make([]byte, 20))
	require.NoError(t, err)
	upper := strings.ToUpper(alice)
	events := []abci.Event{{Type: "x", Attributes: []abci.EventAttribute{
		{Key: "a", Value: alice},
		{Key: "b", Value: alice},
		{Key: "c", Value: "1000norama"},
		{Key: "d", Value: valoper},
		{Key: "e", Value: alice + "," + alice},
		{Key: "f", Value: ""},
		{Key: "g", Value: upper},
	}}}
	require.Equal(t, []string{alice}, eventAddresses(events))
	require.False(t, IsAccountAddress(alice[:len(alice)-1]+"q"), "bad checksum")
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	require.NoError(t, err)
	return b
}

func TestFollower_aFailedDuplicateKeepsTheSuccessfulRecord(t *testing.T) {
	alice, bob := addr(t, 1), addr(t, 2)
	chain := newFakeChain()
	send := bankSend(t, alice, bob)
	chain.add(send)
	chain.add(fakeTx{raw: send.raw, res: &abci.ExecTxResult{Code: 32, Codespace: "sdk", Log: "account sequence mismatch"}})
	store := index(t, chain, 1)
	got, ok, err := store.Tx(send.raw.Hash())
	require.NoError(t, err)
	require.True(t, ok)
	require.Zero(t, got.Code)
	require.Equal(t, int64(1), got.Height)
}

func TestFollower_skipsTheInjectedInclusionCommit(t *testing.T) {
	ctx := context.Background()
	alice, bob := addr(t, 1), addr(t, 2)
	chain := newFakeChain()
	chain.add()
	// From the height inclusion lists switch on, the proposer puts the previous commit first in every
	// block; the chain answers it with an empty successful result (OramaApp.FinalizeBlock).
	injected := fakeTx{
		raw: cmttypes.Tx(inclusion.InjectedCommitMagic + "commit bytes"),
		res: &abci.ExecTxResult{Code: 0, Log: "inclusion-list extended commit"},
	}
	send := bankSend(t, alice, bob)
	chain.add(injected, send)
	chain.add(injected)

	store := openStore(t, t.TempDir())
	defer store.Close()
	f, err := NewFollower(chain, store, 1)
	require.NoError(t, err)
	n, err := f.Step(ctx)
	require.NoError(t, err, "a block that starts with the injected commit is indexed")
	require.Equal(t, 3, n)

	b, ok, err := store.Block(2)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, 1, b.TxCount, "the injected commit is not a transaction")
	require.Equal(t, []string{hex.EncodeToString(send.raw.Hash())}, b.TxHashes)
	only, ok, err := store.Block(3)
	require.NoError(t, err)
	require.True(t, ok)
	require.Zero(t, only.TxCount)
	require.NotNil(t, only.TxHashes)

	got, ok, err := store.Tx(send.raw.Hash())
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, uint32(1), got.Index, "the transaction keeps its place in the block")
	_, ok, err = store.Tx(injected.raw.Hash())
	require.NoError(t, err)
	require.False(t, ok, "the injected commit has no transaction record")

	latest, err := store.LatestTxs(10)
	require.NoError(t, err)
	require.Len(t, latest, 1)
	st, err := store.Status()
	require.NoError(t, err)
	require.Equal(t, int64(3), st.Cursor)
}
