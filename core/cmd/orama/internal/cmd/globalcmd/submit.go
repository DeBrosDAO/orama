package globalcmd

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/clusterreg"
	"github.com/DeBrosOfficial/network/pkg/httputil"
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
		if in, err = withTimeoutHeight(ctx, node, in); err != nil {
			return err
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

// withTimeoutHeight sets the transaction's timeout height from the newest block of the chain at
// node, so the signed body carries the last block it may be included in and a node that holds it
// cannot release it later. A chain that cannot say its height is an error: signing without the
// bound is not a fallback.
func withTimeoutHeight(ctx context.Context, node string, in clusterreg.Direct) (clusterreg.Direct, error) {
	timeout, err := clusterreg.FetchTimeoutHeight(ctx, node)
	if err != nil {
		return in, chainErr(err, "read the newest block's height for the timeout height")
	}
	in.TimeoutHeight = timeout
	return in, nil
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
//
// The hash the node answers must be the hash of the bytes that were sent. One that is not would
// have the wait, and the report, follow some other transaction.
func broadcastAndWait(ctx context.Context, node string, tx []byte, verb string) error {
	answered, err := clusterreg.Broadcast(ctx, node, tx)
	if err != nil {
		return chainErr(err, "broadcast the transaction")
	}
	hash := clusterreg.TxHash(tx)
	if !strings.EqualFold(answered, hash) {
		return clierr.Failure("the node answered the transaction hash %q for a transaction whose hash is %s; refusing to follow or report it",
			httputil.Printable(answered), hash)
	}
	height, err := clusterreg.WaitIncluded(ctx, node, hash, clusterreg.InclusionTimeout, clusterreg.InclusionPoll)
	if err != nil {
		return chainErr(err, "transaction %s", hash)
	}
	fmt.Fprintf(os.Stdout, "%s: %s (block %d)\n", verb, hash, height)
	return nil
}
