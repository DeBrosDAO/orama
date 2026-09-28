package clustercmd

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

// registerFlags are orama cluster register-onchain. The message is
// MsgRegisterCluster: a public row, with no node address and no secret.
type registerFlags struct {
	operator  string
	id        string
	domain    string
	endpoints []string
	metadata  string
	chainID   string
	pubKey    string
	account   uint64
	sequence  uint64
	fee       string
	gas       uint64
	node      string
}

var reg registerFlags

var registerCmd = &cobra.Command{
	Use:   "register-onchain",
	Short: "Register this cluster's public name on the Orama chain",
	Long: `Register an optional public row for this cluster on the Orama chain.

The row is the operator, the cluster id, the base domain, the public
endpoints, and an optional metadata URI. It does not include node addresses,
tenants, or any cluster secret, and registering it does not join a node.

--node is that chain's REST API. The command reads the account there, builds
a SIGN_MODE_DIRECT transaction, asks the RootWallet agent to sign that one
transaction, and broadcasts it. Without --node it prints the sign document
and does not submit anything.

The fee is an explicit amount of norama. There is no default.`,
	Args: cobra.NoArgs,
	RunE: runRegister,
}

func init() {
	f := registerCmd.Flags()
	f.StringVar(&reg.operator, "operator", "", "Operator account (orama1...) [required]")
	f.StringVar(&reg.id, "id", "", "Cluster id [required]")
	f.StringVar(&reg.domain, "base-domain", "", "Public base domain [required]")
	f.StringArrayVar(&reg.endpoints, "endpoint", nil, "Public endpoint (repeatable) [required]")
	f.StringVar(&reg.metadata, "metadata-uri", "", "HTTPS metadata URI")
	f.StringVar(&reg.chainID, "chain-id", "", "Chain id [required]")
	f.StringVar(&reg.pubKey, "pubkey", "", "Compressed secp256k1 pubkey hex; required when the account has not signed before")
	f.Uint64Var(&reg.account, "account-number", 0, "Account number, when not read from --node")
	f.Uint64Var(&reg.sequence, "sequence", 0, "Account sequence, when not read from --node")
	f.StringVar(&reg.fee, "fee", "", "Fee in norama [required]")
	f.Uint64Var(&reg.gas, "gas", 0, "Gas limit [required]")
	f.StringVar(&reg.node, "node", "", "Chain REST API, for example http://127.0.0.1:31003")
	Cmd.AddCommand(registerCmd)
}

func runRegister(cmd *cobra.Command, args []string) error {
	regn := clusterreg.Registration{
		Operator: reg.operator, ClusterID: reg.id, BaseDomain: reg.domain,
		Endpoints: reg.endpoints, MetadataURI: reg.metadata,
	}
	if err := clusterreg.Validate(regn); err != nil {
		return clierr.Usage("%v", err)
	}
	in := clusterreg.SignInput{
		Registration: regn, FeeAmount: reg.fee, Gas: reg.gas, ChainID: reg.chainID,
		AccountNumber: reg.account, Sequence: reg.sequence,
	}
	if reg.pubKey != "" {
		pub, err := hex.DecodeString(reg.pubKey)
		if err != nil {
			return clierr.Usage("pubkey is not hex")
		}
		in.PubKey = pub
	}
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	if reg.node != "" {
		acct, err := clusterreg.FetchAccount(ctx, reg.node, reg.operator)
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
	if reg.node == "" {
		fmt.Fprintf(os.Stdout, "sign document (not submitted):\n%x\n", doc)
		return nil
	}
	client := rwagent.New(os.Getenv("RW_AGENT_SOCK"))
	sig, err := client.SignOramaTx(ctx, doc)
	if err != nil {
		return clierr.Failure("sign the registration: %w", err)
	}
	if sig.Address != reg.operator || hex.EncodeToString(sig.PubKey) != hex.EncodeToString(in.PubKey) {
		return clierr.Failure("the agent signed as %s, not the operator", sig.Address)
	}
	tx, err := in.TxRaw(sig.Signature)
	if err != nil {
		return clierr.Failure("build the transaction: %w", err)
	}
	hash, err := clusterreg.Broadcast(ctx, reg.node, tx)
	if err != nil {
		return clierr.Failure("%v", err)
	}
	fmt.Fprintf(os.Stdout, "registered %s: %s\n", reg.id, hash)
	return nil
}
