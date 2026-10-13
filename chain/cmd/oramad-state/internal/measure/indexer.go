package measure

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/DeBrosOfficial/network/chain/client/node"
	"github.com/DeBrosOfficial/network/chain/cmd/oramad-state/internal/stategen"
	"github.com/DeBrosOfficial/network/chain/indexer"
)

// HistoryBudget bounds how long the chain may take to produce the requested history.
const HistoryBudget = 12 * time.Hour

// IndexerResult is how fast the chain indexer's follower catches up on a history of blocks.
type IndexerResult struct {
	Home string `json:"home"`
	// Blocks is the history the follower indexed, from height 1: at least the number asked for, as
	// the chain keeps producing while it runs.
	Blocks int64 `json:"blocks"`
	// ProduceSeconds is how long the localnet took to make them; BlocksPerSecondProduced is the rate
	// it made them at, set by timeout_commit.
	ProduceSeconds          float64 `json:"produce_seconds"`
	BlocksPerSecondProduced float64 `json:"blocks_per_second_produced"`
	// CatchUpSeconds is the follower's run over the whole history, in steps of
	// indexer.MaxBlocksPerStep blocks.
	CatchUpSeconds float64 `json:"catch_up_seconds"`
	Steps          int     `json:"steps"`
	// BlocksPerSecond is Blocks over CatchUpSeconds: the catch-up rate.
	BlocksPerSecond float64 `json:"blocks_per_second"`
	IndexBytes      int64   `json:"index_bytes"`
}

// Indexer starts a single-node chain on the home, waits until it has committed blocks blocks, then
// runs the indexer's follower from height 1 to the tip and times it. The blocks are whatever the
// chain makes (empty ones on a fresh localnet), so this measures the follower's per-block work and
// the node's RPC, not the cost of busy blocks. The node stays up for the follower, and stops after.
func Indexer(ctx context.Context, o Oramad, home string, blocks int64, rpcPort, p2pPort int) (IndexerResult, error) {
	res := IndexerResult{Home: home, Blocks: blocks}
	node0, err := o.StartNode(home, rpcPort, p2pPort)
	if err != nil {
		return res, err
	}
	runErr := followHistory(ctx, node0, blocks, &res)
	if stopErr := node0.Stop(); runErr == nil {
		runErr = stopErr
	}
	return res, runErr
}

func followHistory(ctx context.Context, n *Node, blocks int64, res *IndexerResult) error {
	if _, err := n.WaitRPC(ctx, BootBudget); err != nil {
		return err
	}
	first := time.Now()
	if _, err := n.WaitHeight(ctx, blocks, HistoryBudget); err != nil {
		return err
	}
	res.ProduceSeconds = time.Since(first).Seconds()
	if res.ProduceSeconds > 0 {
		res.BlocksPerSecondProduced = float64(blocks) / res.ProduceSeconds
	}
	dir, err := os.MkdirTemp("", "oramad-state-index-")
	if err != nil {
		return fmt.Errorf("failed to make the index directory: %w", err)
	}
	defer os.RemoveAll(dir)
	return catchUp(ctx, n.RPC, dir, blocks, res)
}

// catchUp runs the follower over [1, blocks] against the node at rpc, with its index in dir.
func catchUp(ctx context.Context, rpc, dir string, blocks int64, res *IndexerResult) error {
	client, err := node.Dial("http://" + rpc)
	if err != nil {
		return err
	}
	store, err := indexer.Open(dir)
	if err != nil {
		return fmt.Errorf("failed to open the index: %w", err)
	}
	defer store.Close()
	follower, err := indexer.NewFollower(client, store, 1)
	if err != nil {
		return err
	}
	start := time.Now()
	done := int64(0)
	for done < blocks {
		n, err := follower.Step(ctx)
		if err != nil {
			return fmt.Errorf("the follower failed after %d blocks: %w", done, err)
		}
		if n == 0 {
			return fmt.Errorf("the follower stopped at %d of %d blocks", done, blocks)
		}
		done += int64(n)
		res.Steps++
	}
	res.CatchUpSeconds = time.Since(start).Seconds()
	res.Blocks = done
	res.BlocksPerSecond = float64(done) / res.CatchUpSeconds
	res.IndexBytes, err = stategen.DirSize(dir)
	return err
}
