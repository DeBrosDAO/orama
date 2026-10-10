package chaincmd

import (
	"fmt"
	"math/big"

	"github.com/spf13/cobra"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/printer"
	"github.com/DeBrosOfficial/network/pkg/onchain"
)

// sendFlags are the flags of `orama chain send`.
var sendFlags struct{ public, yes bool }

const confirmWord = "yes"

var sendCmd = &cobra.Command{
	Use:   "send <to> <amount> [--public]",
	Short: "Send ORAMA, privately by default; --public sends openly",
	Long: `Send ORAMA to another account. <amount> is in ORAMA, with up to nine decimals
(12, 0.5, 0.000000001).

A send is PRIVATE unless you say otherwise: value moves inside the shielded pool,
and the chain shows no sender, recipient or amount. A private send needs the
RootWallet to build the shielded bundle, and this RootWallet cannot yet (its agent
refuses shielded messages), so today a send without --public stops with that
explanation. It never turns into a public payment on its own.

--public is the only way to pay openly, and it is a choice you make each time:
the sender, the recipient and the amount are then visible on the chain to everyone,
permanently. The command prints the payment and that warning and asks you to type
"yes"; --yes skips the question for scripts. The RootWallet then shows the
transaction and asks you to approve it.

<to> is an orama1... account for a public send. Withdraw earnings to your balance
first ('orama chain withdraw-earnings'); earnings cannot be sent directly.

Transactions go through the gateway of the selected network, or --node (a chain
REST API, for example one reached over an SSH tunnel). Either must be https, or on
this machine. The wallet signs only for the chain the selected network names (from
its registry manifest): an endpoint that answers another chain id is refused, and a
network that names none needs --chain-id. The fee is worked out from the chain and
shown before you confirm; one over --max-fee (1 ORAMA unless you raise it) is
refused before it is signed.

  orama chain send orama1fvfzzvqv2ara2crn3z352zjhnfl0tw4rk82j53 12.5 --public
  orama chain send orama1fvfzzvqv2ara2crn3z352zjhnfl0tw4rk82j53 0.5 --public --yes`,
	Args: cobra.ExactArgs(2),
	RunE: runSend,
}

func init() {
	f := sendCmd.Flags()
	f.BoolVar(&sendFlags.public, "public", false, "Send publicly: the sender, recipient and amount are visible on the chain")
	f.BoolVar(&sendFlags.yes, "yes", false, "Do not ask before a public send")
	addTxFlags(sendCmd)
	Cmd.AddCommand(sendCmd)
}

// sendReport is what `orama chain send --public` prints in JSON.
type sendReport struct {
	txReceipt
	Privacy string `json:"privacy"`
	From    string `json:"from"`
	To      string `json:"to"`
	Amount  string `json:"amount_norama"`
	Warning string `json:"warning"`
}

func runSend(cmd *cobra.Command, args []string) error {
	to := args[0]
	if !sendFlags.public {
		return clierr.Failure("%v", onchain.ErrPrivateUnavailable)
	}
	if err := requireRecipient(to); err != nil {
		return err
	}
	amount, err := parseOramaAmount(args[1])
	if err != nil {
		return err
	}
	client, err := openClient(cmd)
	if err != nil {
		return err
	}
	from, err := client.Operator(cmd.Context())
	if err != nil {
		return txFailure(err)
	}
	prepared, err := client.PreparePublicSend(cmd.Context(), to, amount.String())
	if err != nil {
		return txFailure(err)
	}
	p := printer.For(cmd)
	if err := confirmPublic(cmd, client.chainID, from, to, amount, prepared.Fee); err != nil {
		return err
	}
	receipt, err := prepared.Submit(cmd.Context())
	if err != nil {
		return txFailure(err)
	}
	return printSend(p, sendReport{
		txReceipt: receiptOf(client, receipt), Privacy: "public", From: from, To: to,
		Amount: amount.String(), Warning: onchain.PublicWarning,
	}, amount)
}

// confirmPublic shows the public payment and its warning, and unless --yes asks for the word.
func confirmPublic(cmd *cobra.Command, chainID, from, to string, amount *big.Int, fee string) error {
	w := cmd.ErrOrStderr()
	feeNorama, _ := new(big.Int).SetString(fee, 10)
	fmt.Fprintf(w, "Public transfer on %s\n  from    %s\n  to      %s\n  amount  %s ORAMA (%s norama)\n  fee     %s ORAMA (%s norama)\n%s\n",
		chainID, from, to, orama(amount), amount, orama(feeNorama), fee, onchain.PublicWarning)
	if sendFlags.yes {
		return nil
	}
	fmt.Fprintf(w, "Type %q to send it: ", confirmWord)
	return clierr.Confirm(cmd.InOrStdin(), confirmWord)
}

func printSend(p *printer.Printer, r sendReport, amount *big.Int) error {
	if p.JSONMode() {
		return p.JSON(r)
	}
	p.Ok("sent %s ORAMA publicly to %s", orama(amount), r.To)
	p.Printf("tx:  %s (block %d)\n", r.TxHash, r.Height)
	p.Warn("%s", onchain.PublicWarning)
	return nil
}
