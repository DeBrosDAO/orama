// Package runner runs a profile against a localnet: it dials the nodes, funds the bench accounts,
// prepares and calibrates each transaction type, drives the steps, and collects the report.
package runner

import (
	"context"
	"errors"
	"fmt"

	wasmtypes "github.com/CosmWasm/wasmd/x/wasm/types"
	cmtbytes "github.com/cometbft/cometbft/libs/bytes"
	cmttypes "github.com/cometbft/cometbft/types"

	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/client/node"
	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/apphash"
	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/ops"
	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/watch"
	cnfttypes "github.com/DeBrosOfficial/network/chain/x/cnft/types"
	emissiontypes "github.com/DeBrosOfficial/network/chain/x/emission/types"
	markettypes "github.com/DeBrosOfficial/network/chain/x/market/types"
	nodestypes "github.com/DeBrosOfficial/network/chain/x/nodes/types"
	shieldedtypes "github.com/DeBrosOfficial/network/chain/x/shielded/types"
	tokentypes "github.com/DeBrosOfficial/network/chain/x/token/types"
)

// registrations are the modules whose messages the bench signs or decodes.
func registrations() []func(codectypes.InterfaceRegistry) {
	return []func(codectypes.InterfaceRegistry){
		banktypes.RegisterInterfaces, emissiontypes.RegisterInterfaces, tokentypes.RegisterInterfaces,
		nodestypes.RegisterInterfaces, cnfttypes.RegisterInterfaces, markettypes.RegisterInterfaces,
		wasmtypes.RegisterInterfaces, shieldedtypes.RegisterInterfaces,
	}
}

// Cluster is the nodes of the localnet under test.
type Cluster struct {
	Addrs   []string
	Clients []*node.Client
	// Chain is the signing view over the first node.
	Chain   *ops.Chain
	ChainID string
}

// Dial connects to every node and checks they are one chain.
func Dial(ctx context.Context, addrs []string, feeMultiple int64, gasAdjust uint64) (*Cluster, error) {
	if len(addrs) == 0 {
		return nil, errors.New("no node address given")
	}
	c := &Cluster{Addrs: addrs}
	for _, addr := range addrs {
		client, err := node.DialWith(addr, registrations()...)
		if err != nil {
			return nil, err
		}
		id, _, err := client.Status(ctx)
		if err != nil {
			return nil, fmt.Errorf("node %s is not answering: %w", addr, err)
		}
		if err := requireLocalnet(id); err != nil {
			return nil, fmt.Errorf("node %s: %w", addr, err)
		}
		if c.ChainID != "" && id != c.ChainID {
			return nil, fmt.Errorf("node %s is on chain %q, the first node is on %q", addr, id, c.ChainID)
		}
		c.ChainID = id
		c.Clients = append(c.Clients, client)
	}
	chain, err := ops.New(c.Clients[0], c.ChainID, feeMultiple, gasAdjust)
	if err != nil {
		return nil, err
	}
	c.Chain = chain
	return c, nil
}

// The load generator signs with keys anyone can derive from a public seed, so it refuses any chain
// whose id is not exactly a localnet id (scripts/localnet/localnet.sh, params.ClassOfChainID).

// ErrNotLocalnet is returned when a node is on a chain that is not a localnet.
var ErrNotLocalnet = errors.New("oramad-bench only runs against a localnet")

func requireLocalnet(chainID string) error {
	if params.ClassOfChainID(chainID) != params.ChainLocalnet {
		return fmt.Errorf("%w: chain id %q is not of the form orama-localnet-<name>; the bench accounts' keys are derived from a public seed and must never sign for a real network", ErrNotLocalnet, chainID)
	}
	return nil
}

// nodeView adapts a node to the watchers and the app hash comparison.
type nodeView struct{ client *node.Client }

// LatestHeight implements watch.BlockSource.
func (n nodeView) LatestHeight(ctx context.Context) (int64, error) { return n.client.LatestHeight(ctx) }

// Block implements watch.BlockSource: the block's transactions with their execution results.
func (n nodeView) Block(ctx context.Context, height int64) (watch.BlockData, error) {
	blk, err := n.client.Block(ctx, height)
	if err != nil {
		return watch.BlockData{}, err
	}
	data := watch.BlockData{Height: height, Time: blk.Block.Time}
	if len(blk.Block.Txs) == 0 {
		return data, nil
	}
	res, err := n.client.BlockResults(ctx, height)
	if err != nil {
		return watch.BlockData{}, err
	}
	if len(res.TxsResults) != len(blk.Block.Txs) {
		return watch.BlockData{}, fmt.Errorf("block %d has %d transactions and %d results", height, len(blk.Block.Txs), len(res.TxsResults))
	}
	for i, raw := range blk.Block.Txs {
		r := res.TxsResults[i]
		data.Txs = append(data.Txs, watch.TxResult{Hash: txHash(raw), Code: r.Code, Log: r.Log})
		data.GasUsed += r.GasUsed
		data.GasWanted += r.GasWanted
	}
	return data, nil
}

// Unconfirmed implements watch.MempoolReader.
func (n nodeView) Unconfirmed(ctx context.Context) (int, int64, error) {
	res, err := n.client.RPC().NumUnconfirmedTxs(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("read the mempool size: %w", err)
	}
	return res.Total, res.TotalBytes, nil
}

// header implements the app hash fetcher for one node.
func (c *Cluster) header(ctx context.Context, addr string, height int64) (apphash.HeaderView, error) {
	for i, a := range c.Addrs {
		if a != addr {
			continue
		}
		res, err := c.Clients[i].RPC().Header(ctx, &height)
		if err != nil {
			return apphash.HeaderView{}, err
		}
		h := res.Header
		return apphash.HeaderView{
			Node: addr, Height: h.Height, AppHash: h.AppHash, LastResultsHash: h.LastResultsHash,
			ValidatorsHash: h.ValidatorsHash, BlockHash: h.Hash(),
		}, nil
	}
	return apphash.HeaderView{}, fmt.Errorf("no client for node %s", addr)
}

// txHash is the hash CometBFT identifies a transaction by: uppercase hex of its SHA-256.
func txHash(raw []byte) string { return cmtbytes.HexBytes(cmttypes.Tx(raw).Hash()).String() }
