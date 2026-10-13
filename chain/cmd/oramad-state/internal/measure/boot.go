package measure

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	genutiltypes "github.com/cosmos/cosmos-sdk/x/genutil/types"

	"github.com/DeBrosOfficial/network/chain/cmd/oramad-state/internal/stategen"
)

// BootBudget bounds a boot measurement: a node that holds the full-scale state loads and checks
// every nullifier before it serves its first request.
const BootBudget = 6 * time.Hour

// BootResult is the time a node takes to come up on a home.
type BootResult struct {
	Home string `json:"home"`
	// StartHeight is the height the genesis file makes the node's first block.
	StartHeight int64 `json:"first_block_height"`
	// RPCSeconds is from the process start to the first answer of /status: the application has
	// loaded its stores and checked the nullifier database against the committed state.
	RPCSeconds float64 `json:"rpc_up_seconds"`
	// FirstBlockSeconds is from the process start to the first block the node commits. CometBFT
	// waits timeout_commit after it starts before it proposes that block, so the wait is in it.
	FirstBlockSeconds float64 `json:"first_block_seconds"`
	// TimeoutCommitSeconds is that wait, from the home's config.toml.
	TimeoutCommitSeconds float64 `json:"timeout_commit_seconds"`
	// FirstBlockAfterWaitSeconds is FirstBlockSeconds less the wait: the boot itself.
	FirstBlockAfterWaitSeconds float64        `json:"first_block_less_timeout_commit_seconds"`
	Sizes                      stategen.Sizes `json:"sizes"`
}

// Boot starts a single-node chain on the home and times it to its first committed block. It stops the
// node afterwards. The home must not be in use.
func Boot(ctx context.Context, o Oramad, home string, rpcPort, p2pPort int) (BootResult, error) {
	res := BootResult{Home: home}
	genesis, err := genutiltypes.AppGenesisFromFile(filepath.Join(home, "config", "genesis.json"))
	if err != nil {
		return res, fmt.Errorf("failed to read the genesis of %s: %w", home, err)
	}
	res.StartHeight = max(genesis.InitialHeight, 1)
	if res.Sizes, err = stategen.MeasureSizes(home); err != nil {
		return res, err
	}
	cfg, err := stategen.LoadCometConfig(home)
	if err != nil {
		return res, err
	}
	res.TimeoutCommitSeconds = cfg.Consensus.TimeoutCommit.Seconds()
	node, err := o.StartNode(home, rpcPort, p2pPort)
	if err != nil {
		return res, err
	}
	rpc, rpcErr := node.WaitRPC(ctx, BootBudget)
	if rpcErr == nil {
		res.RPCSeconds = rpc.Seconds()
		var block time.Duration
		if block, rpcErr = node.WaitHeight(ctx, res.StartHeight, BootBudget); rpcErr == nil {
			res.FirstBlockSeconds = block.Seconds()
			res.FirstBlockAfterWaitSeconds = max(block.Seconds()-res.TimeoutCommitSeconds, 0)
		}
	}
	if stopErr := node.Stop(); rpcErr == nil {
		rpcErr = stopErr
	}
	return res, rpcErr
}
