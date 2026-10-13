package indexer

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"cosmossdk.io/math"
	abci "github.com/cometbft/cometbft/abci/types"
	coretypes "github.com/cometbft/cometbft/rpc/core/types"
	cmttypes "github.com/cometbft/cometbft/types"
	gogoproto "github.com/cosmos/gogoproto/proto"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"

	"github.com/DeBrosOfficial/network/chain/x/inclusion"
)

// MaxBlocksPerStep bounds one Step, so a follower far behind the tip still
// returns and logs progress between batches.
const MaxBlocksPerStep = 500

// ErrPruned means the next block to index is below the earliest block the
// node still serves. The follower does not skip it: the index would silently
// miss every transaction in the gap.
var ErrPruned = errors.New("block is pruned on this node")

// Chain is what the follower reads from oramad. *node.Client implements it.
type Chain interface {
	HeightRange(ctx context.Context) (int64, int64, error)
	Block(ctx context.Context, height int64) (*coretypes.ResultBlock, error)
	BlockResults(ctx context.Context, height int64) (*coretypes.ResultBlockResults, error)
	// QueryAt runs a query against the state committed at height (0 is the latest). A node that no
	// longer keeps that state answers with an error that has a StatePruned() bool method returning
	// true (*node.QueryError); the aggregates tell that from any other failure.
	QueryAt(ctx context.Context, height int64, method string, req, resp gogoproto.Message) error
	// ValidatorAddresses returns the consensus addresses of the validator set of a height, in the
	// order of a commit's signatures.
	ValidatorAddresses(ctx context.Context, height int64) ([][]byte, error)
}

// Follower indexes committed blocks in order from a start height.
type Follower struct {
	chain Chain
	store *Store
	start int64
	codec *codec.ProtoCodec
	// signing is the validator set the last commit was read against.
	signing signerSet
	// folding is whether the block being indexed is folded into the aggregates: they are running,
	// or the node still keeps the state their baseline reads. It is set for each block.
	folding bool
	// shielded is what the block being indexed has put in the note-commitment tree.
	shielded blockShielded
}

// NewFollower binds store to start. A store that was started from another
// height is refused.
func NewFollower(chain Chain, store *Store, start int64) (*Follower, error) {
	if start < 1 {
		return nil, fmt.Errorf("start height must be at least 1, got %d", start)
	}
	if err := store.bindStart(start); err != nil {
		return nil, err
	}
	return &Follower{chain: chain, store: store, start: start, codec: newCodec()}, nil
}

// Step indexes the committed blocks after the cursor, at most
// MaxBlocksPerStep of them, and returns how many it indexed. A pass that
// fails leaves the cursor where it was, and the index's status says why the
// follower is stuck; a pass that works clears it.
func (f *Follower) Step(ctx context.Context) (int, error) {
	n, next, err := f.step(ctx)
	switch {
	case err == nil:
		f.store.clearStuck()
	case ctx.Err() == nil:
		f.store.markStuck(next+int64(n), err)
	}
	return n, err
}

// step is one pass of Step. next is the first height it set out to index.
func (f *Follower) step(ctx context.Context) (n int, next int64, err error) {
	st, err := f.store.Status()
	if err != nil {
		return 0, 0, err
	}
	next = st.Cursor + 1
	if st.Cursor == 0 {
		next = f.start
	}
	earliest, latest, err := f.chain.HeightRange(ctx)
	if err != nil {
		return 0, next, err
	}
	if next > latest {
		return 0, next, nil
	}
	if next < earliest {
		return 0, next, fmt.Errorf("%w: the index needs block %d and the node's earliest block is %d; point --rpc at a node that keeps block %d, or index from --start-height %d or later with a new --home",
			ErrPruned, next, earliest, next, earliest)
	}
	for h := next; h <= latest && n < MaxBlocksPerStep; h++ {
		if err := f.indexBlock(ctx, h); err != nil {
			return n, next, err
		}
		n++
	}
	return n, next, nil
}

func (f *Follower) indexBlock(ctx context.Context, height int64) error {
	blk, err := f.chain.Block(ctx, height)
	if err != nil {
		return err
	}
	res, err := f.chain.BlockResults(ctx, height)
	if err != nil {
		return err
	}
	if blk.Block == nil {
		return fmt.Errorf("node returned no block at height %d", height)
	}
	txs := blk.Block.Txs
	if len(res.TxsResults) != len(txs) {
		return fmt.Errorf("block %d has %d transactions but %d results", height, len(txs), len(res.TxsResults))
	}
	w := f.store.newWriter()
	f.shielded.reset()
	if f.folding, err = f.canFold(ctx, w, height); err != nil {
		return errors.Join(fmt.Errorf("block %d: %w", height, err), w.close())
	}
	block := Block{
		Height:   height,
		Hash:     lowerHex(blk.BlockID.Hash),
		Time:     blk.Block.Time,
		Proposer: lowerHex(blk.Block.ProposerAddress),
		TxHashes: make([]string, 0, len(txs)),
	}
	burned := math.ZeroInt()
	for i, raw := range txs {
		if inclusion.IsInjectedCommit(raw) {
			// The proposer's extended commit (C13), not a transaction of anyone: its bytes are no
			// Cosmos transaction by design, and it is not listed or counted. Indexes below keep
			// the place CometBFT gives each transaction in the block.
			continue
		}
		pos := txPos{height: height, index: uint32(i), time: block.Time}
		hash, txBurned, err := f.indexTx(ctx, w, pos, raw, res.TxsResults[i])
		if err != nil {
			return errors.Join(fmt.Errorf("index tx %d of block %d: %w", i, height, err), w.close())
		}
		block.TxHashes = append(block.TxHashes, hash)
		block.GasUsed += res.TxsResults[i].GasUsed
		burned = burned.Add(txBurned)
	}
	if err := f.flushShielded(w, height); err != nil {
		return errors.Join(fmt.Errorf("index the shielded actions of block %d: %w", height, err), w.close())
	}
	block.TxCount = len(block.TxHashes)
	block.Burned = burned.String()
	block.CommitSigned, block.CommitSize = commitTally(blk.Block.LastCommit)
	if err := w.putBlock(block); err != nil {
		return errors.Join(err, w.close())
	}
	facts := blockFacts{height: height, time: block.Time, commit: blk.Block.LastCommit, valsHash: blk.Block.ValidatorsHash, events: res.FinalizeBlockEvents}
	if err := f.aggregate(ctx, w, facts); err != nil {
		return errors.Join(fmt.Errorf("aggregate block %d: %w", height, err), w.close())
	}
	return w.commit(height)
}

// txPos is where a transaction sits: its block and place in it, and the time of
// the block.
type txPos struct {
	height int64
	index  uint32
	time   time.Time
}

// indexTx writes one transaction and returns its hash and the base fee it burned.
func (f *Follower) indexTx(ctx context.Context, w *writer, pos txPos, raw cmttypes.Tx, res *abci.ExecTxResult) (string, math.Int, error) {
	if res == nil {
		return "", math.Int{}, errors.New("node returned no result")
	}
	hash := raw.Hash()
	hashHex := hex.EncodeToString(hash)
	t := Tx{
		Hash: hashHex, Height: pos.height, Index: pos.index, Time: pos.time,
		Code: res.Code, Codespace: res.Codespace, Log: res.Log,
		GasWanted: res.GasWanted, GasUsed: res.GasUsed,
		Messages: []string{}, Events: toEvents(res.Events), Body: []json.RawMessage{},
	}
	env, err := readEnvelope(f.codec, raw)
	switch {
	case err == nil:
		t.Messages = typeURLs(env.msgs)
		t.Signer, t.Memo, t.Body = env.signer, env.memo, env.body
	case res.Code == 0:
		// A block holds only what the application accepted; a successful
		// transaction the index cannot decode means the decoder is wrong.
		return "", math.Int{}, fmt.Errorf("successful transaction %s: %w", hashHex, err)
	}
	burned, err := baseFeeBurned(res.Events)
	if err != nil {
		return "", math.Int{}, fmt.Errorf("transaction %s: %w", hashHex, err)
	}
	if err := f.writeTx(w, pos, hash, t, res, burned); err != nil {
		return "", math.Int{}, err
	}
	if res.Code != 0 {
		return hashHex, burned, nil
	}
	if err := f.applyMessages(ctx, w, pos, hashHex, env.msgs, res); err != nil {
		return "", math.Int{}, err
	}
	if err := f.shielded.noteSubtrees(res.Events); err != nil {
		return "", math.Int{}, fmt.Errorf("transaction %s: %w", hashHex, err)
	}
	return hashHex, burned, nil
}

// writeTx stores the transaction and every index that points at it.
func (f *Follower) writeTx(w *writer, pos txPos, hash []byte, t Tx, res *abci.ExecTxResult, burned math.Int) error {
	if err := w.putTxOnce(hash, t); err != nil {
		return err
	}
	if err := w.addLatest(pos.height, pos.index, hash); err != nil {
		return err
	}
	kinds := kindsOf(t.Messages, res.Code != 0)
	if err := w.addKinds(kinds, pos.height, pos.index, hash); err != nil {
		return err
	}
	if err := w.addToBuckets(pos.time, res.Code != 0, burned); err != nil {
		return err
	}
	for _, addr := range eventAddresses(res.Events) {
		if err := w.addAccountTx(addr, pos.height, pos.index, hash); err != nil {
			return err
		}
		if err := w.addAccountKinds(addr, kinds, pos.height, pos.index, hash); err != nil {
			return err
		}
		if err := w.touchAccount(addr, pos.time); err != nil {
			return err
		}
	}
	return nil
}

// applyMessages folds a successful transaction's x/cnft and x/market messages into the asset index.
func (f *Follower) applyMessages(ctx context.Context, w *writer, pos txPos, hashHex string, msgs []*codectypes.Any, res *abci.ExecTxResult) error {
	resps, err := msgResponses(res.Data)
	if err != nil {
		return fmt.Errorf("transaction %s: %w", hashHex, err)
	}
	if len(resps) != len(msgs) {
		return fmt.Errorf("transaction %s has %d messages but %d responses", hashHex, len(msgs), len(resps))
	}
	a := applier{ctx: ctx, chain: f.chain, w: w, height: pos.height, tx: hashHex, events: res.Events, txIndex: pos.index, shielded: &f.shielded}
	for i := range msgs {
		a.msgIndex = i
		if err := a.apply(msgs[i], resps[i]); err != nil {
			return fmt.Errorf("transaction %s message %d (%s): %w", hashHex, i, msgs[i].TypeUrl, err)
		}
	}
	if err := f.noteUnjails(ctx, w, pos, msgs); err != nil {
		return fmt.Errorf("transaction %s: %w", hashHex, err)
	}
	return nil
}

// baseFeeBurned is the "base_fee" attribute of the "tx" event the fee ante handler
// emits, or zero for a transaction that never reached it.
func baseFeeBurned(events []abci.Event) (math.Int, error) {
	for _, e := range events {
		if e.Type != "tx" {
			continue
		}
		for _, a := range e.Attributes {
			if a.Key != "base_fee" {
				continue
			}
			v, ok := math.NewIntFromString(a.Value)
			if !ok {
				return math.Int{}, fmt.Errorf("tx event base_fee %q is not an integer", a.Value)
			}
			return v, nil
		}
	}
	return math.ZeroInt(), nil
}

func lowerHex(b []byte) string { return hex.EncodeToString(b) }
