package chaincmd

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/chainread"
	"github.com/DeBrosOfficial/network/pkg/onchain"
	"github.com/DeBrosOfficial/network/pkg/rwagent"
)

const (
	// nodeInfoPath is a node's Cosmos REST route for its chain id.
	nodeInfoPath = "/cosmos/base/tendermint/v1beta1/node_info"
	// statusRoute is the gateway's chain status route (CometBFT's /status).
	statusRoute = "status"
)

// openClient builds the transaction client of the active RootWallet account: through --node's REST
// API when it is given, through the gateway's public routes otherwise. It is a variable so a test
// can drive the commands with a fake chain and a fake wallet.
var openClient = func(cmd *cobra.Command) (txClient, error) {
	if readFlags.rpc != "" {
		return txClient{}, clierr.Usage("transactions are sent through the gateway or --node, not --rpc")
	}
	r, err := reader(readFlags.node == "")
	if err != nil {
		return txClient{}, err
	}
	var chain onchain.Chain = onchain.Gateway{Reader: r}
	if readFlags.node != "" {
		chain = onchain.REST{Base: readFlags.node}
	}
	id, err := chainID(cmd.Context(), r)
	if err != nil {
		return txClient{}, err
	}
	client, err := onchain.New(chain, rwagent.New(os.Getenv("RW_AGENT_SOCK")), id)
	if err != nil {
		return txClient{}, clierr.Failure("%v", err)
	}
	return txClient{Client: client, chainID: id}, nil
}

// txClient is an onchain.Client and the chain id it signs for.
type txClient struct {
	*onchain.Client
	chainID string
}

// chainID reads the id of the chain the transaction is signed for. It is read from the same chain
// the transaction is sent to, so the wallet never signs for one chain and broadcasts to another.
func chainID(ctx context.Context, r *chainread.Reader) (string, error) {
	var raw json.RawMessage
	var err error
	if r.REST != "" {
		raw, err = r.RESTGet(ctx, nodeInfoPath)
	} else {
		raw, err = r.GatewayGet(ctx, statusRoute)
	}
	if err != nil {
		return "", clierr.Unavailable("read the chain id to sign for: %v", err)
	}
	var doc struct {
		DefaultNodeInfo struct {
			Network string `json:"network"`
		} `json:"default_node_info"`
		Result struct {
			NodeInfo struct {
				Network string `json:"network"`
			} `json:"node_info"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return "", clierr.Failure("the chain answered a status that is not JSON: %v", err)
	}
	id := doc.DefaultNodeInfo.Network
	if id == "" {
		id = doc.Result.NodeInfo.Network
	}
	if strings.TrimSpace(id) == "" {
		return "", clierr.Failure("the chain's status has no chain id")
	}
	return id, nil
}

// txReceipt is what a transaction command prints.
type txReceipt struct {
	TxHash  string `json:"tx_hash"`
	Height  int64  `json:"height"`
	ChainID string `json:"chain_id"`
	Fee     string `json:"fee_norama"`
}

func receiptOf(c txClient, r *onchain.Receipt) txReceipt {
	return txReceipt{TxHash: r.Hash, Height: r.Height, ChainID: c.chainID, Fee: r.Fee}
}

// txFailure classifies a failed transaction for the exit code: a wallet that is locked or refused
// is an authorization problem, a chain that did not answer is retryable, the rest is a failure.
func txFailure(err error) error {
	var agent *rwagent.AgentError
	if errors.As(err, &agent) {
		return clierr.Wrap(clierr.CodeAuth, err)
	}
	return clierr.Failure("%v", err)
}
