package measure

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"time"

	dbm "github.com/cometbft/cometbft-db"
	sm "github.com/cometbft/cometbft/state"
	"github.com/cometbft/cometbft/store"
	"github.com/cometbft/cometbft/types"

	"github.com/DeBrosOfficial/network/chain/cmd/oramad-state/internal/stategen"
	archivetypes "github.com/DeBrosOfficial/network/chain/x/archive/types"
)

const (
	// blockInterval is the spacing of the synthetic blocks' times; it is what the chain targets.
	blockInterval = 5 * time.Second
	// DefaultPruneTxs and DefaultPruneTxBytes shape a synthetic block: ten transactions of a typical
	// signed send's size.
	DefaultPruneTxs     = 10
	DefaultPruneTxBytes = 250
	syntheticChainID    = "orama-localnet-bench-1"
	proposerBytes       = 20
	signatureBytes      = 64
)

// PruneResult is the cost of deleting the blocks x/archive lets a node drop.
type PruneResult struct {
	Blocks      int64 `json:"blocks_in_store"`
	TxsPerBlock int   `json:"txs_per_block"`
	TxBytes     int   `json:"tx_bytes"`
	// Window is the retention window the retain height used, and LastArchived the height x/archive
	// says is archived; the chain's window is archivetypes.DefaultBlocksIn14Days.
	Window       int64 `json:"retention_window_blocks"`
	LastArchived int64 `json:"last_archived_height"`
	RetainHeight int64 `json:"retain_height"`

	BuildSeconds     float64 `json:"build_seconds"`
	Pruned           uint64  `json:"blocks_pruned"`
	PruneSeconds     float64 `json:"prune_seconds"`
	MillisPer1000    float64 `json:"milliseconds_per_1000_blocks"`
	StoreBytesBefore int64   `json:"store_bytes_before"`
	StoreBytesAfter  int64   `json:"store_bytes_after"`
}

// PruneConfig shapes a prune measurement.
type PruneConfig struct {
	WorkDir      string
	Blocks       int64
	TxsPerBlock  int
	TxBytes      int
	Window       int64
	LastArchived int64 // zero: every block is archived
}

// Prune builds a CometBFT block store of synthetic blocks and prunes it the way a node does after a
// commit: CometBFT's BlockStore.PruneBlocks at the height x/archive's rule allows
// (archivetypes.RetainHeight). The store is in WorkDir and is removed afterwards.
func Prune(ctx context.Context, cfg PruneConfig) (PruneResult, error) {
	res := PruneResult{Blocks: cfg.Blocks, TxsPerBlock: cfg.TxsPerBlock, TxBytes: cfg.TxBytes, Window: cfg.Window, LastArchived: cfg.LastArchived}
	if res.LastArchived == 0 {
		res.LastArchived = cfg.Blocks
	}
	res.RetainHeight = archivetypes.RetainHeight(cfg.Blocks, cfg.Window, res.LastArchived)
	if res.RetainHeight <= 0 {
		return res, fmt.Errorf("x/archive's rule lets nothing be pruned with %d blocks, a window of %d and %d archived; use more blocks than the window", cfg.Blocks, cfg.Window, res.LastArchived)
	}
	dir := filepath.Join(cfg.WorkDir, "blockstore")
	defer os.RemoveAll(dir)
	db, err := dbm.NewDB("blockstore", dbm.GoLevelDBBackend, dir)
	if err != nil {
		return res, fmt.Errorf("failed to open the block store: %w", err)
	}
	defer db.Close()
	bs := store.NewBlockStore(db)
	start := time.Now()
	if err := fillBlocks(ctx, bs, cfg); err != nil {
		return res, err
	}
	res.BuildSeconds = time.Since(start).Seconds()
	if res.StoreBytesBefore, err = stategen.DirSize(dir); err != nil {
		return res, err
	}
	params := types.DefaultConsensusParams()
	state := sm.State{LastBlockHeight: cfg.Blocks, LastBlockTime: blockTime(cfg.Blocks), ConsensusParams: *params}
	start = time.Now()
	if res.Pruned, _, err = bs.PruneBlocks(res.RetainHeight, state); err != nil {
		return res, fmt.Errorf("failed to prune the block store: %w", err)
	}
	elapsed := time.Since(start)
	res.PruneSeconds = elapsed.Seconds()
	res.MillisPer1000 = elapsed.Seconds() * 1000 / (float64(res.Pruned) / 1000)
	res.StoreBytesAfter, err = stategen.DirSize(dir)
	return res, err
}

func blockTime(height int64) time.Time {
	return time.Unix(0, 0).UTC().Add(time.Duration(height) * blockInterval)
}

// fillBlocks saves cfg.Blocks contiguous blocks, each with a commit of one validator for the block
// before it, the way the consensus reactor leaves them.
func fillBlocks(ctx context.Context, bs *store.BlockStore, cfg PruneConfig) error {
	prev := types.BlockID{}
	for h := int64(1); h <= cfg.Blocks; h++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		last := &types.Commit{}
		if h > 1 {
			last = commitFor(h-1, prev)
		}
		block := types.MakeBlock(h, syntheticTxs(h, cfg), last, nil)
		block.ChainID, block.Time, block.ProposerAddress = syntheticChainID, blockTime(h), make([]byte, proposerBytes)
		parts, err := block.MakePartSet(types.BlockPartSizeBytes)
		if err != nil {
			return fmt.Errorf("failed to split block %d: %w", h, err)
		}
		prev = types.BlockID{Hash: block.Hash(), PartSetHeader: parts.Header()}
		bs.SaveBlock(block, parts, commitFor(h, prev))
	}
	return nil
}

func commitFor(height int64, id types.BlockID) *types.Commit {
	sig := types.CommitSig{BlockIDFlag: types.BlockIDFlagCommit, ValidatorAddress: make([]byte, proposerBytes),
		Timestamp: blockTime(height), Signature: make([]byte, signatureBytes)}
	return &types.Commit{Height: height, BlockID: id, Signatures: []types.CommitSig{sig}}
}

// syntheticTxs are TxsPerBlock transactions of TxBytes each whose content depends on the height, so
// no two blocks share a data hash.
func syntheticTxs(height int64, cfg PruneConfig) []types.Tx {
	txs := make([]types.Tx, cfg.TxsPerBlock)
	for i := range txs {
		tx := make([]byte, max(cfg.TxBytes, 16))
		binary.BigEndian.PutUint64(tx, uint64(height))
		binary.BigEndian.PutUint64(tx[8:], uint64(i))
		txs[i] = tx
	}
	return txs
}
