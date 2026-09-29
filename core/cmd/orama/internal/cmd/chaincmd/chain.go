// Package chaincmd is `orama chain`: read the Orama chain over HTTP JSON.
//
// It links no chain or Cosmos code. Each command names the read path it uses:
// the gateway's /v1/chain/ proxy, a node's Cosmos REST API (--node), or a
// node's CometBFT RPC (--rpc). The Orama modules' own state is read through
// the gateway's /v1/chain/query/ route, or through --rpc's abci_query. It only reads; transactions are the `orama global`,
// `orama storage` and `orama cluster` commands.
package chaincmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/shared"
	"github.com/DeBrosOfficial/network/pkg/chainread"
)

var readFlags struct{ gateway, node, rpc string }

// Cmd is `orama chain`.
var Cmd = &cobra.Command{
	Use:   "chain",
	Short: "Read the Orama chain: status, balances, earnings, nodes, deals, validators",
	Long: `Read the Orama chain. Every command here only reads.

Three read paths exist, and each command uses one:

  --gateway  the gateway's read-only /v1/chain/ proxy (default: the active
             environment's gateway). Status, blocks, transactions, the
             validator set, supply, the indexer and the Orama module queries
             (x/nodes, x/storage, x/fees, ...) under /v1/chain/query/.
  --node     a node's Cosmos REST API, for example http://127.0.0.1:31003.
             Accounts, bank balances, staking validators.
  --rpc      a node's CometBFT RPC, for example http://127.0.0.1:31001. The
             Orama modules answer gRPC only and abci_query is their one node
             HTTP route; with --rpc set, earnings, node, deal and query read
             it directly instead of through the gateway.

Transactions are built and signed by 'orama global', 'orama storage' and
'orama cluster'; --onion on those submits through Tor.`,
}

func init() {
	f := Cmd.PersistentFlags()
	f.StringVar(&readFlags.gateway, "gateway", "", "Gateway URL for /v1/chain/ reads (default: the active environment's gateway)")
	f.StringVar(&readFlags.node, "node", "", "Chain REST API, for example http://127.0.0.1:31003")
	f.StringVar(&readFlags.rpc, "rpc", "", "CometBFT RPC, for example http://127.0.0.1:31001")
}

// reader builds the Reader from the flags. The gateway URL is resolved only
// when a command asks for it, so --rpc and --node work with no environment.
func reader(needGateway bool) (*chainread.Reader, error) {
	r := &chainread.Reader{REST: readFlags.node, RPC: readFlags.rpc}
	if needGateway {
		gateway, err := shared.GatewayURL(readFlags.gateway)
		if err != nil {
			return nil, clierr.Usage("no gateway to read through: %v (or pass --rpc or --node)", err)
		}
		r.Gateway = gateway
	}
	return r, nil
}

// requireAddress refuses anything that is not shaped like an orama address
// before it becomes part of a URL or a query.
func requireAddress(arg string) error {
	const prefix = "orama1"
	if !strings.HasPrefix(arg, prefix) || strings.ContainsAny(arg, "/?#% ") || len(arg) < len(prefix)+6 {
		return clierr.Usage("%q is not an orama address (orama1...)", arg)
	}
	return nil
}

// printJSON writes raw indented, and a read failure as a failure.
func printJSON(cmd *cobra.Command, raw json.RawMessage, err error) error {
	if err != nil {
		return clierr.Failure("%v", err)
	}
	var out bytes.Buffer
	if err := json.Indent(&out, raw, "", "  "); err != nil {
		return clierr.Failure("the chain answered malformed JSON: %v", err)
	}
	fmt.Fprintln(cmd.OutOrStdout(), out.String())
	return nil
}
