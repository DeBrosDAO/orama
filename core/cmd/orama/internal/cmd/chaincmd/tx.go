package chaincmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"os"

	"github.com/spf13/cobra"

	cli "github.com/DeBrosOfficial/network/cmd/orama/internal"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/chainread"
	"github.com/DeBrosOfficial/network/pkg/httputil"
	"github.com/DeBrosOfficial/network/pkg/onchain"
	"github.com/DeBrosOfficial/network/pkg/rwagent"
)

const (
	// nodeInfoPath is a node's Cosmos REST route for its chain id.
	nodeInfoPath = "/cosmos/base/tendermint/v1beta1/node_info"
	// statusRoute is the gateway's chain status route (CometBFT's /status).
	statusRoute = "status"
)

// txFlags are the flags of the transaction commands that select the chain and bound the cost.
var txFlags struct{ maxFee, chainID string }

// addTxFlags declares the flags every transaction command takes.
func addTxFlags(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringVar(&txFlags.maxFee, "max-fee", "", fmt.Sprintf("Most the transaction may pay in fee, in ORAMA (default %s): a higher fee is refused before it is signed", orama(big.NewInt(onchain.DefaultMaxFeeNorama))))
	f.StringVar(&txFlags.chainID, "chain-id", "", "The chain id you expect, for a network that does not name one (a network from the registry already does); refused if the endpoint runs another")
}

// Seams the tests replace.
var (
	// expectedChainID is the chain id the active network is known to run.
	expectedChainID = cli.ExpectedChainID
	// newSigner is the RootWallet agent.
	newSigner = func() onchain.Signer { return rwagent.New(os.Getenv("RW_AGENT_SOCK")) }
)

// openClient builds the transaction client of the active RootWallet account: through --node's REST
// API when it is given, through the gateway's public routes otherwise. It is a variable so a test
// can drive the commands with a fake chain and a fake wallet.
//
// It signs only for a chain it was told to expect: the network's registry manifest names the chain
// id, and the endpoint must answer it. An endpoint on plain http that is not this machine is
// refused, since the account, the fee and the transaction all travel through it.
var openClient = func(cmd *cobra.Command) (txClient, error) {
	if readFlags.rpc != "" {
		return txClient{}, clierr.Usage("transactions are sent through the gateway or --node, not --rpc")
	}
	r, err := reader(readFlags.node == "")
	if err != nil {
		return txClient{}, err
	}
	endpoint := r.Gateway
	var chain onchain.Chain = onchain.Gateway{Reader: r}
	if readFlags.node != "" {
		endpoint, chain = r.REST, onchain.REST{Base: r.REST}
	}
	if err := requireSecureEndpoint(endpoint); err != nil {
		return txClient{}, err
	}
	want, err := pinnedChainID(txFlags.chainID)
	if err != nil {
		return txClient{}, err
	}
	id, err := chainID(cmd.Context(), r)
	if err != nil {
		return txClient{}, err
	}
	if id != want {
		return txClient{}, clierr.Failure("%s runs the chain %q, not the %q you expect: refusing to sign for it", httputil.Printable(endpoint), id, want)
	}
	client, err := onchain.New(chain, newSigner(), id)
	if err != nil {
		return txClient{}, clierr.Failure("%v", err)
	}
	if txFlags.maxFee != "" {
		limit, err := parseOramaAmount(txFlags.maxFee)
		if err != nil {
			return txClient{}, clierr.Usage("--max-fee: %v", err)
		}
		if err := client.SetMaxFee(limit); err != nil {
			return txClient{}, clierr.Usage("--max-fee: %v", err)
		}
	}
	return txClient{Client: client, chainID: id}, nil
}

// pinnedChainID is the chain the user expects to sign for: the one the active network's manifest
// names, or the one --chain-id says when the network names none. The two may not disagree.
func pinnedChainID(explicit string) (string, error) {
	pinned, network, err := expectedChainID()
	if err != nil {
		return "", clierr.Failure("%v", err)
	}
	switch {
	case pinned != "" && explicit != "" && explicit != pinned:
		return "", clierr.Usage("--chain-id %q is not the chain of network %q, which is %q", explicit, network, pinned)
	case pinned != "":
		return pinned, nil
	case explicit != "":
		return explicit, nil
	}
	return "", clierr.Usage("cannot tell which chain this network runs, so the wallet cannot be asked to sign for it: " +
		"choose a network from the registry ('orama network use <name>') or pass --chain-id <id>")
}

// requireSecureEndpoint refuses to sign through an endpoint that is neither https nor on this
// machine. A transaction's account, fee and hash all come back through it.
func requireSecureEndpoint(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return clierr.Usage("%q is not a URL", httputil.Printable(raw))
	}
	if u.Scheme == "https" {
		return nil
	}
	if u.Scheme == "http" && isLoopback(u.Hostname()) {
		return nil
	}
	return clierr.Usage("%s is not https and not on this machine: refusing to sign through an endpoint an attacker on the path could rewrite", httputil.Printable(raw))
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
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
		return "", clierr.Unavailable("read the chain id to sign for: %v", httputil.Printable(err.Error()))
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
	if !onchain.ValidChainID(id) {
		return "", clierr.Failure("the chain's status has no usable chain id: %q is not 1 to 64 characters of A-Z, a-z, 0-9, '.', '_' and '-'", httputil.Printable(id))
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
	return clierr.Failure("%v", httputil.Printable(err.Error()))
}
