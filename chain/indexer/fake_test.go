package indexer

import (
	"context"
	"crypto/sha256"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	abci "github.com/cometbft/cometbft/abci/types"
	coretypes "github.com/cometbft/cometbft/rpc/core/types"
	cmttypes "github.com/cometbft/cometbft/types"
	gogoproto "github.com/cosmos/gogoproto/proto"

	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/bech32"
	txtypes "github.com/cosmos/cosmos-sdk/types/tx"

	"github.com/DeBrosOfficial/network/chain/app/params"
)

// fakeChain is a node that serves a fixed list of blocks and answers
// queries from a table keyed by method and height.
type fakeChain struct {
	earliest int64
	blocks   []fakeBlock // blocks[i] is height i+1
	queries  map[string]gogoproto.Message
	fetched  []int64
	asked    []string
	// ledger answers the queries the table does not: the chain's economy and validators.
	ledger *fakeLedger
	// valset is the validator set of every height, in commit order; valsetCalls counts the reads of it.
	valset [][]byte
	// valsetChanges maps a height to the set that governs it and the heights after it.
	valsetChanges map[int64][][]byte
	valsetCalls   int
	// step is the seconds between blocks.
	step int64
}

type fakeBlock struct {
	txs     []cmttypes.Tx
	results []*abci.ExecTxResult
	// commit is the block's LastCommit votes; events are its finalize-block events.
	commit []cmttypes.CommitSig
	events []abci.Event
}

func newFakeChain() *fakeChain {
	return &fakeChain{earliest: 1, queries: map[string]gogoproto.Message{}, ledger: newFakeLedger(), step: 1}
}

// addBlock appends a block with commit votes and finalize-block events.
func (f *fakeChain) addBlock(commit []cmttypes.CommitSig, events []abci.Event, txs ...fakeTx) int64 {
	h := f.add(txs...)
	f.blocks[h-1].commit, f.blocks[h-1].events = commit, events
	return h
}

func (f *fakeChain) ValidatorAddresses(_ context.Context, height int64) ([][]byte, error) {
	f.valsetCalls++
	return f.valsetAt(height), nil
}

// valsetAt is the validator set of a height: the latest change at or below it, else the first set.
func (f *fakeChain) valsetAt(height int64) [][]byte {
	set, from := f.valset, int64(0)
	for h, s := range f.valsetChanges {
		if h <= height && h > from {
			set, from = s, h
		}
	}
	return set
}

func (f *fakeChain) valsHash(height int64) []byte {
	h := sha256.New()
	for _, a := range f.valsetAt(height) {
		h.Write(a)
	}
	return h.Sum(nil)
}

func (f *fakeChain) add(txs ...fakeTx) int64 {
	b := fakeBlock{}
	for _, t := range txs {
		b.txs = append(b.txs, t.raw)
		b.results = append(b.results, t.res)
	}
	f.blocks = append(f.blocks, b)
	return int64(len(f.blocks))
}

func (f *fakeChain) HeightRange(context.Context) (int64, int64, error) {
	return f.earliest, int64(len(f.blocks)), nil
}

func (f *fakeChain) Block(_ context.Context, h int64) (*coretypes.ResultBlock, error) {
	if h < f.earliest || h > int64(len(f.blocks)) {
		return nil, fmt.Errorf("height %d is not available", h)
	}
	f.fetched = append(f.fetched, h)
	blk := &cmttypes.Block{
		Header: cmttypes.Header{Height: h, Time: f.timeOf(h), ProposerAddress: make([]byte, 20), ValidatorsHash: f.valsHash(h)},
		Data:   cmttypes.Data{Txs: f.blocks[h-1].txs},
	}
	if h > 1 {
		blk.LastCommit = &cmttypes.Commit{Height: h - 1, Signatures: f.blocks[h-1].commit}
	}
	hash := sha256.Sum256([]byte(fmt.Sprintf("block-%d", h)))
	return &coretypes.ResultBlock{BlockID: cmttypes.BlockID{Hash: hash[:]}, Block: blk}, nil
}

func (f *fakeChain) BlockResults(_ context.Context, h int64) (*coretypes.ResultBlockResults, error) {
	if h < f.earliest || h > int64(len(f.blocks)) {
		return nil, fmt.Errorf("height %d is not available", h)
	}
	return &coretypes.ResultBlockResults{Height: h, TxsResults: f.blocks[h-1].results, FinalizeBlockEvents: f.blocks[h-1].events}, nil
}

// timeOf is the time of block h on this chain: step seconds per block.
func (f *fakeChain) timeOf(h int64) time.Time { return time.Unix(1_700_000_000+h*f.step, 0).UTC() }

// blockTime is the time of block h: a second per block from a fixed origin. Height 0 is the
// chain's genesis time.
func blockTime(h int64) time.Time { return time.Unix(1_700_000_000+h, 0).UTC() }

func queryKey(method string, height int64) string { return fmt.Sprintf("%s@%d", method, height) }

func (f *fakeChain) QueryAt(_ context.Context, height int64, method string, req, resp gogoproto.Message) error {
	key := queryKey(method, height)
	f.asked = append(f.asked, key)
	ans, ok := f.queries[key]
	if !ok {
		var err error
		if ans, err = f.ledger.answer(method, height, req); err != nil {
			return fmt.Errorf("no answer for %s: %w", key, err)
		}
	}
	raw, err := gogoproto.Marshal(ans)
	if err != nil {
		return err
	}
	return gogoproto.Unmarshal(raw, resp)
}

// txSeq makes every test transaction's bytes, and so its hash, distinct.
var txSeq int

// fakeTx is a transaction's bytes and its result.
type fakeTx struct {
	raw cmttypes.Tx
	res *abci.ExecTxResult
}

func anyOf(t *testing.T, m gogoproto.Message) *codectypes.Any {
	t.Helper()
	raw, err := gogoproto.Marshal(m)
	require.NoError(t, err)
	return &codectypes.Any{TypeUrl: "/" + gogoproto.MessageName(m), Value: raw}
}

// okTx is a successful transaction of msgs, answered with resps, whose
// events name addrs the way the SDK's message and transfer events do.
func okTx(t *testing.T, msgs []gogoproto.Message, resps []gogoproto.Message, addrs ...string) fakeTx {
	t.Helper()
	txSeq++
	body := txtypes.TxBody{Memo: fmt.Sprintf("tx-%d", txSeq)}
	for _, m := range msgs {
		body.Messages = append(body.Messages, anyOf(t, m))
	}
	bodyBytes, err := gogoproto.Marshal(&body)
	require.NoError(t, err)
	raw, err := gogoproto.Marshal(&txtypes.TxRaw{BodyBytes: bodyBytes})
	require.NoError(t, err)
	var data sdk.TxMsgData
	for _, r := range resps {
		data.MsgResponses = append(data.MsgResponses, anyOf(t, r))
	}
	dataBytes, err := gogoproto.Marshal(&data)
	require.NoError(t, err)
	return fakeTx{raw: raw, res: &abci.ExecTxResult{Code: 0, Data: dataBytes, GasWanted: 200000, GasUsed: 81234, Events: addrEvents(addrs)}}
}

// failedTx is a transaction the chain ran and refused.
func failedTx(t *testing.T, msgs []gogoproto.Message, addrs ...string) fakeTx {
	t.Helper()
	tx := okTx(t, msgs, nil, addrs...)
	tx.res = &abci.ExecTxResult{Code: 5, Codespace: "sdk", Log: "insufficient funds", GasWanted: 100000, GasUsed: 40000, Events: addrEvents(addrs)}
	return tx
}

func addrEvents(addrs []string) []abci.Event {
	ev := []abci.Event{{Type: "tx", Attributes: []abci.EventAttribute{{Key: "fee", Value: "2000norama"}}}}
	for _, a := range addrs {
		ev = append(ev, abci.Event{Type: "message", Attributes: []abci.EventAttribute{
			{Key: "action", Value: "/orama.cnft.v1.MsgTransfer"},
			{Key: "sender", Value: a},
			{Key: "module", Value: "cnft"},
		}})
	}
	return ev
}

func addr(t *testing.T, seed byte) string {
	t.Helper()
	raw := make([]byte, 20)
	raw[19] = seed
	s, err := bech32.ConvertAndEncode(params.Bech32Prefix, raw)
	require.NoError(t, err)
	return s
}

func assetID(seed byte) []byte {
	sum := sha256.Sum256([]byte{seed})
	return sum[:]
}

func openStore(t *testing.T, dir string) *Store {
	t.Helper()
	s, err := Open(dir)
	require.NoError(t, err)
	return s
}
