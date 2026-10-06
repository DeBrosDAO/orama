package clustercmd

import (
	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/cmd/globalcmd"
	"github.com/DeBrosOfficial/network/pkg/clusterreg"
	"github.com/spf13/cobra"
)

var retireFlags struct {
	chainID  string
	operator string
	id       string
	pubKey   string
	account  uint64
	sequence uint64
	fee      string
	gas      uint64
	node     string
}

var retireClusterCmd = &cobra.Command{
	Use:   "retire-onchain",
	Short: "Retire this cluster's public row on the Orama chain",
	Long: `Retire the optional public cluster row. This does not change any node and
does not delete the cluster's namespaces. Without --node the command prints
the sign document and does not submit it.`,
	Args: cobra.NoArgs,
	RunE: runRetireCluster,
}

func init() {
	f := retireClusterCmd.Flags()
	f.StringVar(&retireFlags.chainID, "chain-id", "", "Chain id [required]")
	f.StringVar(&retireFlags.operator, "operator", "", "Operator account (orama1...) [required]")
	f.StringVar(&retireFlags.id, "id", "", "Cluster id [required]")
	f.StringVar(&retireFlags.pubKey, "pubkey", "", "Compressed secp256k1 pubkey hex of the signing account")
	f.Uint64Var(&retireFlags.account, "account-number", 0, "Account number, when not read from --node")
	f.Uint64Var(&retireFlags.sequence, "sequence", 0, "Account sequence, when not read from --node")
	f.StringVar(&retireFlags.fee, "fee", "", "Fee in norama [required]")
	f.Uint64Var(&retireFlags.gas, "gas", 0, "Gas limit [required]")
	f.StringVar(&retireFlags.node, "node", "", "Chain REST API, for example http://127.0.0.1:31003; the command returns once the transaction is in a block, and fails if the block refuses it")
	globalcmd.AddOnionFlags(f)
	Cmd.AddCommand(retireClusterCmd)
}

func runRetireCluster(cmd *cobra.Command, args []string) error {
	r := clusterreg.Retire{Operator: retireFlags.operator, ID: retireFlags.id}
	if err := clusterreg.ValidateRetire(r); err != nil {
		return clierr.Usage("%v", err)
	}
	in := clusterreg.Direct{
		TypeURL: clusterreg.RetireClusterTypeURL, Msg: clusterreg.EncodeRetire(r),
		FeeAmount: retireFlags.fee, Gas: retireFlags.gas, ChainID: retireFlags.chainID,
		AccountNumber: retireFlags.account, Sequence: retireFlags.sequence,
	}
	return globalcmd.SubmitDirect(cmd, retireFlags.operator, retireFlags.node, retireFlags.pubKey, retireFlags.account, retireFlags.sequence, in, "retired "+retireFlags.id)
}
