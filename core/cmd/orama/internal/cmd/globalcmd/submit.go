package globalcmd

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/clusterreg"
	"github.com/DeBrosOfficial/network/pkg/rwagent"
	"github.com/spf13/cobra"
)

// SubmitDirect fills the account from the chain REST API when node is set,
// prints the sign document when it is empty, and otherwise signs, broadcasts, and waits until the
// transaction is in a block.
// --onion (or ORAMA_CHAIN_ONION) replaces node with a validator onion service
// reached only through Tor: an unreachable proxy or service is an error, never
// a clearnet send.
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
	ctx, node, stopTor, err := chainTarget(cmd, ctx, node)
	if err != nil {
		return err
	}
	defer stopTor()
	if node != "" {
		acct, err := clusterreg.FetchAccount(ctx, node, operator)
		if err != nil {
			return chainErr(err, "read the chain account")
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
	return broadcastAndWait(ctx, node, tx, verb)
}

// chainErr classifies a failure of a chain REST call. A request that got no
// answer (a refused connection, a timeout, a 5xx) is Unavailable, so a script
// may retry it; an answer that says no is a Failure, which retrying will not
// change. what names the step and comes first in the message.
func chainErr(err error, what string, args ...any) error {
	wrapped := fmt.Errorf("%s: %w", fmt.Sprintf(what, args...), err)
	var transport *url.Error
	var status *clusterreg.StatusError
	if errors.As(err, &transport) || (errors.As(err, &status) && status.Code >= http.StatusInternalServerError) {
		return clierr.Wrap(clierr.CodeUnavailable, wrapped)
	}
	return clierr.Wrap(clierr.CodeFailure, wrapped)
}

// broadcastAndWait sends tx and reports it only once it is in a block: admission to the mempool
// is not success, since the block that runs it can still refuse it.
func broadcastAndWait(ctx context.Context, node string, tx []byte, verb string) error {
	hash, err := clusterreg.Broadcast(ctx, node, tx)
	if err != nil {
		return chainErr(err, "broadcast the transaction")
	}
	height, err := clusterreg.WaitIncluded(ctx, node, hash, clusterreg.InclusionTimeout, clusterreg.InclusionPoll)
	if err != nil {
		return chainErr(err, "transaction %s", hash)
	}
	fmt.Fprintf(os.Stdout, "%s: %s (block %d)\n", verb, hash, height)
	return nil
}
