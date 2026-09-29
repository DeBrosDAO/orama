package chaincmd

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/printer"
	"github.com/DeBrosOfficial/network/pkg/chainread"
)

// Query names of the Orama module reads the commands wrap.
const (
	queryNode     = "orama.nodes.v1.Query/Node"
	queryDeal     = "orama.storage.v1.Query/Deal"
	queryEarnings = "orama.fees.v1.Query/Earnings"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the chain's height, network and sync state",
	Long: `Show CometBFT's status: the network id, the latest block and whether the node is
catching up. Reads the gateway's /v1/chain/status, or --rpc's /status.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		r, err := reader(readFlags.rpc == "")
		if err != nil {
			return err
		}
		if readFlags.rpc != "" {
			raw, err := r.RPCGet(cmd.Context(), "/status")
			return printJSON(cmd, raw, err)
		}
		raw, err := r.GatewayGet(cmd.Context(), "status")
		return printJSON(cmd, raw, err)
	},
}

var balanceCmd = &cobra.Command{
	Use:   "balance <address>",
	Short: "Show an account's bank balances",
	Long: `Show an account's bank balances from --node's REST API. This is the account's
spendable bank balance. Earnings live in a separate account: see
'orama chain earnings'.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAddress(args[0]); err != nil {
			return err
		}
		r, err := reader(false)
		if err != nil {
			return err
		}
		raw, err := r.RESTGet(cmd.Context(), "/cosmos/bank/v1beta1/balances/"+chainread.Escape(args[0]))
		if err != nil {
			return clierr.Failure("%v", err)
		}
		if printer.For(cmd).JSONMode() {
			return printJSON(cmd, raw, nil)
		}
		return printBalances(cmd, raw)
	},
}

func printBalances(cmd *cobra.Command, raw json.RawMessage) error {
	var resp struct {
		Balances []struct{ Denom, Amount string } `json:"balances"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return clierr.Failure("the chain answered malformed balances: %v", err)
	}
	if len(resp.Balances) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "no balance")
		return nil
	}
	for _, b := range resp.Balances {
		fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", b.Amount, b.Denom)
	}
	return nil
}

var earningsCmd = &cobra.Command{
	Use:   "earnings <address>",
	Short: "Show an account's earnings balance (x/fees)",
	Long: `Show the earnings balance x/fees holds for an account, through --rpc. Earnings
are what the account is paid for running nodes and services; they are not in
the bank balance.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAddress(args[0]); err != nil {
			return err
		}
		return grpcRead(cmd, queryEarnings, map[string]string{"address": args[0]})
	},
}

var nodeCmd = &cobra.Command{
	Use:   "node <node-id>",
	Short: "Show a registered node (x/nodes)",
	Long:  `Show a node's record from x/nodes through --rpc: operator, roles, bonds, endpoints, capacity and status.`,
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return grpcRead(cmd, queryNode, map[string]string{"node_id": args[0]})
	},
}

var dealCmd = &cobra.Command{
	Use:   "deal <deal-id>",
	Short: "Show a storage deal (x/storage)",
	Long:  `Show a storage deal from x/storage through --rpc.`,
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := strconv.ParseUint(args[0], 10, 64)
		if err != nil {
			return clierr.Usage("deal id %q is not a number", args[0])
		}
		return grpcRead(cmd, queryDeal, map[string]string{"deal_id": strconv.FormatUint(id, 10)})
	},
}

var validatorCmd = &cobra.Command{
	Use:   "validator [oramavaloper-address]",
	Short: "List the validator set, or show one validator",
	Long: `Without an argument, list the CometBFT validator set from the gateway's
/v1/chain/validators (or --rpc's /validators). With an oramavaloper address,
show that validator's staking record from --node's REST API.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 1 {
			if err := requireValoper(args[0]); err != nil {
				return err
			}
			r, err := reader(false)
			if err != nil {
				return err
			}
			raw, err := r.RESTGet(cmd.Context(), "/cosmos/staking/v1beta1/validators/"+chainread.Escape(args[0]))
			return printJSON(cmd, raw, err)
		}
		r, err := reader(readFlags.rpc == "")
		if err != nil {
			return err
		}
		if readFlags.rpc != "" {
			raw, err := r.RPCGet(cmd.Context(), "/validators")
			return printJSON(cmd, raw, err)
		}
		raw, err := r.GatewayGet(cmd.Context(), "validators")
		return printJSON(cmd, raw, err)
	},
}

func requireValoper(arg string) error {
	if len(arg) < len("oramavaloper1")+6 || arg[:len("oramavaloper1")] != "oramavaloper1" {
		return clierr.Usage("%q is not an oramavaloper address (oramavaloper1...)", arg)
	}
	return nil
}

var queryCmd = &cobra.Command{
	Use:   "query <Service/Method> [request-json]",
	Short: "Run any Orama module query through --rpc",
	Long: `Run a gRPC query of an Orama module through --rpc's abci_query and print the
response as JSON. The request is JSON with the proto field names. For example:

  orama chain query orama.nodes.v1.Query/Node '{"node_id":"node-1"}' --rpc http://127.0.0.1:31001

'orama chain query --list' prints every query the CLI knows.`,
	Args: cobra.RangeArgs(0, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		if list, _ := cmd.Flags().GetBool("list"); list {
			methods, err := chainread.Methods()
			if err != nil {
				return clierr.Failure("%v", err)
			}
			for _, m := range methods {
				fmt.Fprintln(cmd.OutOrStdout(), m)
			}
			return nil
		}
		if len(args) == 0 {
			return clierr.Usage("name a query, for example orama.nodes.v1.Query/Node, or pass --list")
		}
		request := ""
		if len(args) == 2 {
			request = args[1]
		}
		r, err := reader(false)
		if err != nil {
			return err
		}
		raw, err := r.GRPC(cmd.Context(), args[0], request)
		return printJSON(cmd, raw, err)
	},
}

// grpcRead runs one Orama module query with a request built from fields.
func grpcRead(cmd *cobra.Command, query string, fields map[string]string) error {
	request, err := json.Marshal(fields)
	if err != nil {
		return clierr.Failure("%v", err)
	}
	r, err := reader(false)
	if err != nil {
		return err
	}
	raw, err := r.GRPC(cmd.Context(), query, string(request))
	return printJSON(cmd, raw, err)
}

func init() {
	queryCmd.Flags().Bool("list", false, "List every query the CLI knows and exit")
	Cmd.AddCommand(statusCmd, balanceCmd, earningsCmd, nodeCmd, dealCmd, validatorCmd, queryCmd)
}
