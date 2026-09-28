package globalcmd

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/clusterreg"
	"github.com/DeBrosOfficial/network/pkg/rwagent"
	"github.com/spf13/cobra"
)

// submitDirect fills the account from the chain REST API when --node is set,
// prints the sign document when it is not, and otherwise signs and broadcasts.
// SubmitDirect fills the account from the chain REST API when node is set,
// prints the sign document when it is empty, and otherwise signs and broadcasts.
func SubmitDirect(cmd *cobra.Command, operator, node, pubHex string, account, sequence uint64, in clusterreg.Direct, verb string) error {
	if pubHex != "" {
		pub, err := hex.DecodeString(pubHex)
		if err != nil {
			return clierr.Usage("pubkey is not hex")
		}
		in.PubKey = pub
	}
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	if node != "" {
		acct, err := clusterreg.FetchAccount(ctx, node, operator)
		if err != nil {
			return clierr.Failure("read the chain account: %w", err)
		}
		if !cmd.Flags().Changed("account-number") {
			in.AccountNumber = acct.Number
		}
		if !cmd.Flags().Changed("sequence") {
			in.Sequence = acct.Sequence
		}
		if len(in.PubKey) == 0 {
			in.PubKey = acct.PubKey
		}
	}
	doc, err := in.SignDoc()
	if err != nil {
		return clierr.Usage("%v", err)
	}
	if node == "" {
		fmt.Fprintf(os.Stdout, "sign document (not submitted):\n%x\n", doc)
		return nil
	}
	client := rwagent.New(os.Getenv("RW_AGENT_SOCK"))
	sig, err := client.SignOramaTx(ctx, doc)
	if err != nil {
		return clierr.Failure("sign the transaction: %w", err)
	}
	if sig.Address != operator || hex.EncodeToString(sig.PubKey) != hex.EncodeToString(in.PubKey) {
		return clierr.Failure("the agent signed as %s, not the operator", sig.Address)
	}
	tx, err := in.TxRaw(sig.Signature)
	if err != nil {
		return clierr.Failure("build the transaction: %w", err)
	}
	hash, err := clusterreg.Broadcast(ctx, node, tx)
	if err != nil {
		return clierr.Failure("%v", err)
	}
	fmt.Fprintf(os.Stdout, "%s: %s\n", verb, hash)
	return nil
}
