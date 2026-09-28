package storagecmd

import (
	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/cmd/globalcmd"
	"github.com/DeBrosOfficial/network/pkg/clusterreg"
	"github.com/spf13/cobra"
)

// Cmd is orama storage.
var Cmd = &cobra.Command{
	Use:   "storage",
	Short: "Storage deals on the Orama chain",
}

var grantFlags struct {
	chainID  string
	signer   string
	grantee  string
	spend    string
	period   uint64
	piece    uint64
	duration uint64
	replicas uint32
	expiry   uint64
	pubKey   string
	account  uint64
	sequence uint64
	fee      string
	gas      uint64
	node     string
}

func init() {
	grant := &cobra.Command{
		Use:   "grant",
		Short: "Grant a cluster a capped deal allowance",
		Long: `Grant a deal allowance to another account.

The grant is not SDK authz. It caps spend, piece size, duration, and replica
count. Without --node the command prints the sign document and does not submit it.`,
		Args: cobra.NoArgs,
		RunE: runGrant,
	}
	revoke := &cobra.Command{
		Use:   "revoke",
		Short: "Revoke a deal allowance",
		Long:  `Revoke a deal allowance. Without --node the command prints the sign document and does not submit it.`,
		Args:  cobra.NoArgs,
		RunE:  runRevoke,
	}
	add := func(c *cobra.Command) {
		f := c.Flags()
		f.StringVar(&grantFlags.chainID, "chain-id", "", "Chain id [required]")
		f.StringVar(&grantFlags.signer, "signer", "", "Granter account (orama1...) [required]")
		f.StringVar(&grantFlags.grantee, "grantee", "", "Grantee account (orama1...) [required]")
		f.StringVar(&grantFlags.pubKey, "pubkey", "", "Compressed secp256k1 pubkey hex of the signing account")
		f.Uint64Var(&grantFlags.account, "account-number", 0, "Account number, when not read from --node")
		f.Uint64Var(&grantFlags.sequence, "sequence", 0, "Account sequence, when not read from --node")
		f.StringVar(&grantFlags.fee, "fee", "", "Fee in norama [required]")
		f.Uint64Var(&grantFlags.gas, "gas", 0, "Gas limit [required]")
		f.StringVar(&grantFlags.node, "node", "", "Chain REST API, for example http://127.0.0.1:31003")
	}
	add(grant)
	add(revoke)
	grant.Flags().StringVar(&grantFlags.spend, "spend-limit", "", "Spend limit in norama [required]")
	grant.Flags().Uint64Var(&grantFlags.period, "period-epochs", 0, "Epochs in one spend period")
	grant.Flags().Uint64Var(&grantFlags.piece, "max-piece-bytes", 0, "Largest piece the grant allows [required]")
	grant.Flags().Uint64Var(&grantFlags.duration, "max-duration-epochs", 0, "Longest deal the grant allows [required]")
	grant.Flags().Uint32Var(&grantFlags.replicas, "replicas", 3, "Exact replica count a deal must use")
	grant.Flags().Uint64Var(&grantFlags.expiry, "expiry-epoch", 0, "Epoch after which the grant is dead")
	Cmd.AddCommand(grant, revoke)
}

func runGrant(cmd *cobra.Command, args []string) error {
	g := clusterreg.Grant{
		Signer: grantFlags.signer, Grantee: grantFlags.grantee, SpendLimit: grantFlags.spend,
		PeriodEpochs: grantFlags.period, MaxPieceBytes: grantFlags.piece, MaxDuration: grantFlags.duration,
		Replicas: grantFlags.replicas, ExpiryEpoch: grantFlags.expiry,
	}
	if err := clusterreg.ValidateGrant(g); err != nil {
		return clierr.Usage("%v", err)
	}
	in := clusterreg.Direct{
		TypeURL: clusterreg.GrantDealTypeURL, Msg: clusterreg.EncodeGrant(g),
		FeeAmount: grantFlags.fee, Gas: grantFlags.gas, ChainID: grantFlags.chainID,
		AccountNumber: grantFlags.account, Sequence: grantFlags.sequence,
	}
	return globalcmd.SubmitDirect(cmd, grantFlags.signer, grantFlags.node, grantFlags.pubKey, grantFlags.account, grantFlags.sequence, in, "granted "+grantFlags.grantee)
}

func runRevoke(cmd *cobra.Command, args []string) error {
	if _, err := clusterreg.CanonicalAccount(grantFlags.signer); err != nil {
		return clierr.Usage("signer: %v", err)
	}
	if _, err := clusterreg.CanonicalAccount(grantFlags.grantee); err != nil {
		return clierr.Usage("grantee: %v", err)
	}
	in := clusterreg.Direct{
		TypeURL: clusterreg.RevokeDealTypeURL, Msg: clusterreg.EncodeRevokeGrant(grantFlags.signer, grantFlags.grantee),
		FeeAmount: grantFlags.fee, Gas: grantFlags.gas, ChainID: grantFlags.chainID,
		AccountNumber: grantFlags.account, Sequence: grantFlags.sequence,
	}
	return globalcmd.SubmitDirect(cmd, grantFlags.signer, grantFlags.node, grantFlags.pubKey, grantFlags.account, grantFlags.sequence, in, "revoked "+grantFlags.grantee)
}
