package indexer

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"

	abci "github.com/cometbft/cometbft/abci/types"
	coretypes "github.com/cometbft/cometbft/rpc/core/types"
	cmttypes "github.com/cometbft/cometbft/types"
	gogoproto "github.com/cosmos/gogoproto/proto"
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
	QueryAt(ctx context.Context, height int64, method string, req, resp gogoproto.Message) error
}

// Follower indexes committed blocks in order from a start height.
type Follower struct {
	chain Chain
	store *Store
	start int64
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
	return &Follower{chain: chain, store: store, start: start}, nil
}

// Step indexes the committed blocks after the cursor, at most
// MaxBlocksPerStep of them, and returns how many it indexed.
func (f *Follower) Step(ctx context.Context) (int, error) {
	st, err := f.store.Status()
	if err != nil {
		return 0, err
	}
	next := st.Cursor + 1
	if st.Cursor == 0 {
		next = f.start
	}
	earliest, latest, err := f.chain.HeightRange(ctx)
	if err != nil {
		return 0, err
	}
	if next > latest {
		return 0, nil
	}
	if next < earliest {
		return 0, fmt.Errorf("%w: the index needs block %d and the node's earliest block is %d; point --rpc at a node that keeps block %d, or index from --start-height %d or later with a new --home",
			ErrPruned, next, earliest, next, earliest)
	}
	n := 0
	for h := next; h <= latest && n < MaxBlocksPerStep; h++ {
		if err := f.indexBlock(ctx, h); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
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
	block := Block{
		Height:   height,
		Hash:     lowerHex(blk.BlockID.Hash),
		Time:     blk.Block.Time,
		Proposer: lowerHex(blk.Block.ProposerAddress),
		TxCount:  len(txs),
		TxHashes: make([]string, 0, len(txs)),
	}
	for i, raw := range txs {
		hash, err := f.indexTx(ctx, w, height, uint32(i), raw, res.TxsResults[i])
		if err != nil {
			return errors.Join(fmt.Errorf("index tx %d of block %d: %w", i, height, err), w.close())
		}
		block.TxHashes = append(block.TxHashes, hash)
	}
	if err := w.putBlock(block); err != nil {
		return errors.Join(err, w.close())
	}
	return w.commit(height)
}

func (f *Follower) indexTx(ctx context.Context, w *writer, height int64, index uint32, raw cmttypes.Tx, res *abci.ExecTxResult) (string, error) {
	if res == nil {
		return "", errors.New("node returned no result")
	}
	hash := raw.Hash()
	hashHex := hex.EncodeToString(hash)
	t := Tx{
		Hash: hashHex, Height: height, Index: index,
		Code: res.Code, Codespace: res.Codespace, Log: res.Log,
		GasWanted: res.GasWanted, GasUsed: res.GasUsed,
		Messages: []string{}, Events: toEvents(res.Events),
	}
	msgs, err := bodyMessages(raw)
	switch {
	case err == nil:
		t.Messages = typeURLs(msgs)
	case res.Code == 0:
		// A block holds only what the application accepted; a successful
		// transaction the index cannot decode means the decoder is wrong.
		return "", fmt.Errorf("successful transaction %s: %w", hashHex, err)
	}
	if err := w.putTxOnce(hash, t); err != nil {
		return "", err
	}
	for _, addr := range eventAddresses(res.Events) {
		if err := w.addAccountTx(addr, height, index, hash); err != nil {
			return "", err
		}
	}
	if res.Code != 0 {
		return hashHex, nil
	}
	resps, err := msgResponses(res.Data)
	if err != nil {
		return "", fmt.Errorf("transaction %s: %w", hashHex, err)
	}
	if len(resps) != len(msgs) {
		return "", fmt.Errorf("transaction %s has %d messages but %d responses", hashHex, len(msgs), len(resps))
	}
	a := applier{ctx: ctx, chain: f.chain, w: w, height: height, tx: hashHex}
	for i := range msgs {
		if err := a.apply(msgs[i], resps[i]); err != nil {
			return "", fmt.Errorf("transaction %s message %d (%s): %w", hashHex, i, msgs[i].TypeUrl, err)
		}
	}
	return hashHex, nil
}

func lowerHex(b []byte) string { return hex.EncodeToString(b) }
