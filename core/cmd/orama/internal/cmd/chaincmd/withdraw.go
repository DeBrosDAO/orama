package chaincmd

import (
	"math/big"

	"github.com/spf13/cobra"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/printer"
)

var withdrawCmd = &cobra.Command{
	Use:   "withdraw-earnings <amount>",
	Short: "Move earnings to your own balance, where they can be sent",
	Long: `Move <amount> ORAMA of your earnings to your own bank balance
(MsgWithdrawEarnings). <amount> is in ORAMA, with up to nine decimals.

Earnings are what your nodes are paid; they sit in a separate account that cannot
be sent from. Withdrawing makes them spendable: the amount is the one thing you
choose, the destination is always your own account, and the chain refuses an
amount above your earnings. See what you have with 'orama chain earnings <address>'.

Then use them as you choose: send them publicly ('orama chain send <to> <amount>
--public'), or move them into the shielded pool to keep them private (this needs a
RootWallet that builds shielded bundles). Withdrawing is itself visible on the
chain: it shows that this account withdrew this amount.

The RootWallet shows the transaction and asks you to approve it. Transactions go
through the gateway of the selected network, or --node, over https or on this
machine, and the wallet signs only for the chain the selected network names (see
'orama chain send' for --chain-id and --max-fee).

  orama chain withdraw-earnings 25`,
	Args: cobra.ExactArgs(1),
	RunE: runWithdraw,
}

func init() {
	addTxFlags(withdrawCmd)
	Cmd.AddCommand(withdrawCmd)
}

// withdrawReport is what `orama chain withdraw-earnings` prints in JSON.
type withdrawReport struct {
	txReceipt
	Account string `json:"account"`
	Amount  string `json:"amount_norama"`
}

func runWithdraw(cmd *cobra.Command, args []string) error {
	amount, err := parseOramaAmount(args[0])
	if err != nil {
		return err
	}
	client, err := openClient(cmd)
	if err != nil {
		return err
	}
	account, err := client.Operator(cmd.Context())
	if err != nil {
		return txFailure(err)
	}
	receipt, err := client.WithdrawEarnings(cmd.Context(), amount.String())
	if err != nil {
		return txFailure(err)
	}
	return printWithdraw(printer.For(cmd), withdrawReport{
		txReceipt: receiptOf(client, receipt), Account: account, Amount: amount.String(),
	}, amount)
}

func printWithdraw(p *printer.Printer, r withdrawReport, amount *big.Int) error {
	if p.JSONMode() {
		return p.JSON(r)
	}
	p.Ok("withdrew %s ORAMA of earnings to the balance of %s", orama(amount), r.Account)
	p.Printf("tx:  %s (block %d)\n", r.TxHash, r.Height)
	return nil
}
