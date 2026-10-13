package runner

import (
	"context"
	"encoding/hex"
	"fmt"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/client/tx"
	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/ops"
	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/shielded"
	shieldedtypes "github.com/DeBrosOfficial/network/chain/x/shielded/types"
)

// shieldedChain is the chain as shielded.Build sees it.
type shieldedChain struct {
	cluster  *Cluster
	accounts []tx.Account
	parallel int
}

var _ shielded.Chain = (*shieldedChain)(nil)

func (c *shieldedChain) ChainID() string { return c.cluster.ChainID }

func (c *shieldedChain) GenesisHash(ctx context.Context) (string, error) {
	blk, err := c.cluster.Clients[0].Block(ctx, 1)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(blk.BlockID.Hash), nil
}

func (c *shieldedChain) Params(ctx context.Context) (shielded.Params, error) {
	var resp shieldedtypes.QueryParamsResponse
	if err := c.cluster.Clients[0].Query(ctx, "/orama.shielded.v1.Query/Params", &shieldedtypes.QueryParamsRequest{}, &resp); err != nil {
		return shielded.Params{}, err
	}
	p := resp.Params
	return shielded.Params{
		ActionGas: p.ActionGas, NullifierFee: p.NullifierFee, MaxActions: p.MaxActionsPerBundle,
		MaxFeeTopup: p.MaxFeeTopup, AnchorWindow: p.AnchorWindowBlocks,
	}, nil
}

func (c *shieldedChain) GasPrice(ctx context.Context) (math.Int, error) {
	return c.cluster.Chain.GasPrice(ctx)
}

// Tip reads the head and the note tree at that same height, so the two agree.
func (c *shieldedChain) Tip(ctx context.Context) (shielded.Tip, error) {
	client := c.cluster.Clients[0]
	height, err := client.LatestHeight(ctx)
	if err != nil {
		return shielded.Tip{}, err
	}
	var tree shieldedtypes.QueryTreeStateResponse
	if err := client.QueryAt(ctx, height, "/orama.shielded.v1.Query/TreeState", &shieldedtypes.QueryTreeStateRequest{}, &tree); err != nil {
		return shielded.Tip{}, fmt.Errorf("read the note tree at height %d: %w", height, err)
	}
	return shielded.Tip{Height: height, TreeSize: tree.TreeSize, Root: hex.EncodeToString(tree.CurrentRoot)}, nil
}

// SubmitShields sends each bundle as a MsgShield from a bench account, the accounts working in
// parallel and each one's bundles in turn. Submit waits for the block and fails on a failed execution.
func (c *shieldedChain) SubmitShields(ctx context.Context, bundles [][]byte) error {
	n := len(c.accounts)
	return ops.Each(ctx, n, c.parallel, func(ctx context.Context, a int) error {
		for i := a; i < len(bundles); i += n {
			msg := &shieldedtypes.MsgShield{Signer: c.accounts[a].Address, Bundle: bundles[i]}
			if _, err := c.cluster.Clients[0].Submit(ctx, c.accounts[a], msg); err != nil {
				return fmt.Errorf("shield bundle %d from %s: %w", i, c.accounts[a].Address, err)
			}
		}
		return nil
	})
}

func (c *shieldedChain) SignerAddress(i int) (string, error) {
	if i < 0 || i >= len(c.accounts) {
		return "", fmt.Errorf("bench account %d does not exist (there are %d)", i, len(c.accounts))
	}
	addr, err := sdk.AccAddressFromBech32(c.accounts[i].Address)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(addr), nil
}

func (c *shieldedChain) Blocks() shielded.BlockSource { return chainBlocks{c.cluster} }

// chainBlocks reads the transactions of blocks for the note tree scan.
type chainBlocks struct{ cluster *Cluster }

func (b chainBlocks) Txs(ctx context.Context, height int64) ([]shielded.TxRecord, error) {
	client := b.cluster.Clients[0]
	blk, err := client.Block(ctx, height)
	if err != nil {
		return nil, err
	}
	if len(blk.Block.Txs) == 0 {
		return nil, nil
	}
	res, err := client.BlockResults(ctx, height)
	if err != nil {
		return nil, err
	}
	if len(res.TxsResults) != len(blk.Block.Txs) {
		return nil, fmt.Errorf("block %d has %d transactions and %d results", height, len(blk.Block.Txs), len(res.TxsResults))
	}
	decode := client.TxConfig().TxDecoder()
	out := make([]shielded.TxRecord, len(blk.Block.Txs))
	for i, raw := range blk.Block.Txs {
		out[i].Code = res.TxsResults[i].Code
		parsed, err := decode(raw)
		if err != nil {
			// A transaction of a module this client does not register (a validator's own report, say)
			// cannot carry a shielded message. If the scan nevertheless missed a bundle, the tree-size
			// check in shielded.Build catches it.
			continue
		}
		out[i].Msgs = parsed.GetMsgs()
	}
	return out, nil
}
