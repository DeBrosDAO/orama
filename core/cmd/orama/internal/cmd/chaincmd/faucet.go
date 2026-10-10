package chaincmd

import (
	"fmt"
	"math/big"

	"github.com/spf13/cobra"

	"github.com/DeBrosOfficial/network/cmd/orama/internal"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/noderesolver"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/printer"
	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/inspector"
	"github.com/DeBrosOfficial/network/pkg/remotessh"
)

var faucetFlags struct{ env, node, amount string }

// Seams the tests replace: the node inventory and the SSH keys.
var (
	resolveNodes = noderesolver.ResolveNodes
	prepareKeys  = remotessh.PrepareNodeKeys
)

var faucetCmd = &cobra.Command{
	Use:   "faucet <recipient>",
	Short: "Fund an account on a test network (stagenet, devnet)",
	Long: `Send test ORAMA to an account from the chain's faucet (MsgFaucet).

The faucet exists only on a test network: a chain whose id contains -stagenet-,
-devnet- or -localnet- and whose genesis turned it on (faucet_enabled). This
command reads the chain id from the node first and refuses any other chain
before it signs anything. The chain refuses a drip over its maximum and a second
drip to the same recipient inside its cooldown (24 hours by default).

The transaction is signed ON a node, with the node's operator key (the
"validator" key of oramad's test keyring), over SSH with the environment's
wallet-provided key. The key never leaves the node and nothing secret is
printed. The operator pays the fee from its earnings. The recipient need not
exist yet. --amount is in norama (1 ORAMA = 1000000000 norama).

Prints the transaction hash, the amount, and the recipient's bank balance once
the transaction is in a block.

  orama chain faucet orama1fvfzzvqv2ara2crn3z352zjhnfl0tw4rk82j53 --env stagenet
  orama chain faucet <address> --env stagenet --amount 5000000000 --node 57.129.166.16

--node is the SSH host of the node that signs (here it is not the REST URL the
other 'orama chain' commands take); without it the environment's first node
signs.`,
	Args: cobra.ExactArgs(1),
	RunE: runFaucet,
}

func init() {
	f := faucetCmd.Flags()
	f.StringVar(&faucetFlags.env, "env", "", "Environment whose node signs (default: the active environment)")
	f.StringVar(&faucetFlags.node, "node", "", "SSH host (IP) of the node that signs (default: the environment's first node)")
	f.StringVar(&faucetFlags.amount, "amount", faucetDefaultAmount, "Amount of norama to send (1 ORAMA = 1000000000 norama)")
	Cmd.AddCommand(faucetCmd)
}

// faucetReport is what the command prints.
type faucetReport struct {
	TxHash    string `json:"tx_hash"`
	Height    string `json:"height"`
	ChainID   string `json:"chain_id"`
	Recipient string `json:"recipient"`
	Amount    string `json:"amount_norama"`
	Balance   string `json:"recipient_balance_norama"`
}

func runFaucet(cmd *cobra.Command, args []string) error {
	recipient := args[0]
	if err := requireRecipient(recipient); err != nil {
		return err
	}
	amount, err := parseFaucetAmount(faucetFlags.amount)
	if err != nil {
		return err
	}
	node, cleanup, err := faucetNode(faucetFlags.env, faucetFlags.node)
	if err != nil {
		return err
	}
	defer cleanup()
	rep, err := sendFaucet(node, recipient, amount)
	if err != nil {
		return err
	}
	return printFaucet(printer.For(cmd), rep, amount)
}

// faucetNode picks the node that signs and loads the SSH key for it.
func faucetNode(env, host string) (inspector.Node, func(), error) {
	if env == "" {
		active, err := cli.GetActiveEnvironment()
		if err != nil {
			return inspector.Node{}, nil, clierr.Usage("no --env given and no active environment: %v", err)
		}
		env = active.Name
	}
	nodes, err := resolveNodes(env)
	if err != nil {
		return inspector.Node{}, nil, fmt.Errorf("failed to resolve the nodes of %s: %w", env, err)
	}
	node, err := pickNode(nodes, host, env)
	if err != nil {
		return inspector.Node{}, nil, err
	}
	picked := []inspector.Node{node}
	cleanup, err := prepareKeys(picked)
	if err != nil {
		return inspector.Node{}, nil, fmt.Errorf("failed to resolve the SSH key for %s: %w", node.Host, err)
	}
	return picked[0], cleanup, nil
}

// pickNode is the node named by host, or the first one when none is named.
func pickNode(nodes []inspector.Node, host, env string) (inspector.Node, error) {
	if len(nodes) == 0 {
		return inspector.Node{}, clierr.NotFound("the %s environment has no nodes", env)
	}
	if host == "" {
		return nodes[0], nil
	}
	for _, n := range nodes {
		if n.Host == host {
			return n, nil
		}
	}
	return inspector.Node{}, clierr.NotFound("node %s is not in the %s environment", host, env)
}

// sendFaucet probes the node, refuses a chain that is not a test network, signs and broadcasts the
// drip on the node, and reads its block and the recipient's balance.
func sendFaucet(node inspector.Node, recipient string, amount *big.Int) (faucetReport, error) {
	out, err := remoteScript(node, probeScript())
	if err != nil {
		return faucetReport{}, fmt.Errorf("failed to read the chain and the %s key on %s (does it run %s, and does its keyring hold that key?): %w",
			faucetOperatorKey, node.Host, constants.ChainServiceUnit, err)
	}
	probe, err := parseProbe(out)
	if err != nil {
		return faucetReport{}, fmt.Errorf("failed to read the chain on %s: %w", node.Host, err)
	}
	if err := requireTestNetwork(probe.ChainID); err != nil {
		return faucetReport{}, err
	}
	unsigned, err := unsignedFaucetTx(probe.Signer, recipient, amount)
	if err != nil {
		return faucetReport{}, err
	}
	out, err = remoteScript(node, faucetTxScript(probe.ChainID, unsigned))
	if err != nil {
		return faucetReport{}, fmt.Errorf("failed to sign and broadcast the faucet transaction on %s: %w", node.Host, err)
	}
	sent, err := parseBroadcast(out)
	if err != nil {
		return faucetReport{}, err
	}
	out, err = remoteScript(node, resultScript(sent.TxHash, recipient))
	if err != nil {
		return faucetReport{}, fmt.Errorf("failed to read transaction %s from %s: %w", sent.TxHash, node.Host, err)
	}
	delivered, balance, err := parseResult(out)
	if err != nil {
		return faucetReport{}, err
	}
	return faucetReport{TxHash: sent.TxHash, Height: delivered.Height, ChainID: probe.ChainID,
		Recipient: recipient, Amount: amount.String(), Balance: balance.String()}, nil
}

func printFaucet(p *printer.Printer, r faucetReport, amount *big.Int) error {
	if p.JSONMode() {
		return p.JSON(r)
	}
	bal, _ := new(big.Int).SetString(r.Balance, 10)
	p.Ok("sent %s norama (%s ORAMA) to %s on %s", r.Amount, orama(amount), r.Recipient, r.ChainID)
	p.Printf("tx:      %s (height %s)\n", r.TxHash, r.Height)
	p.Printf("balance: %s norama (%s ORAMA)\n", r.Balance, orama(bal))
	return nil
}
