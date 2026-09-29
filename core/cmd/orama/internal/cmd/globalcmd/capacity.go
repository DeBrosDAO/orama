package globalcmd

import (
	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/clusterreg"
	"github.com/spf13/cobra"
)

var capacityFlags struct {
	chainID  string
	operator string
	id       string
	bytes    uint64
	pubKey   string
	account  uint64
	sequence uint64
	fee      string
	gas      uint64
	node     string
}

var capacityCmd = &cobra.Command{
	Use:   "capacity",
	Short: "Declare how many bytes a storage node will hold",
	Long: `Declare the storage capacity of a registered node that has the storage role.

The chain refuses a declaration above the capacity the role bond backs, and
below the bytes already reserved by deals. Zero is a declaration of no
capacity. Without --node the command prints the sign document and does not
submit it.`,
	Args: cobra.NoArgs,
	RunE: runCapacity,
}

var retireNodeCmd = &cobra.Command{
	Use:   "retire",
	Short: "Retire a global node",
	Long: `Retire a global node. The chain records its service pubkeys so they cannot
be bound again. Without --node the command prints the sign document and does
not submit it.`,
	Args: cobra.NoArgs,
	RunE: runRetireNode,
}

func init() {
	addChainFlags := func(f interface {
		StringVar(*string, string, string, string)
		Uint64Var(*uint64, string, uint64, string)
		String(name, value, usage string) *string
	}, idHelp string) {
		f.StringVar(&capacityFlags.chainID, "chain-id", "", "Chain id [required]")
		f.StringVar(&capacityFlags.operator, "operator", "", "Operator account (orama1...) [required]")
		f.StringVar(&capacityFlags.id, "id", "", idHelp)
		f.StringVar(&capacityFlags.pubKey, "pubkey", "", "Compressed secp256k1 pubkey hex of the signing account")
		f.Uint64Var(&capacityFlags.account, "account-number", 0, "Account number, when not read from --node")
		f.Uint64Var(&capacityFlags.sequence, "sequence", 0, "Account sequence, when not read from --node")
		f.StringVar(&capacityFlags.fee, "fee", "", "Fee in norama [required]")
		f.Uint64Var(&capacityFlags.gas, "gas", 0, "Gas limit [required]")
		f.StringVar(&capacityFlags.node, "node", "", "Chain REST API, for example http://127.0.0.1:31003")
		AddOnionFlags(f)
	}
	addChainFlags(capacityCmd.Flags(), "Node id [required]")
	capacityCmd.Flags().Uint64Var(&capacityFlags.bytes, "bytes", 0, "Declared capacity in bytes")
	addChainFlags(retireNodeCmd.Flags(), "Node id [required]")
	Cmd.AddCommand(capacityCmd)
	Cmd.AddCommand(retireNodeCmd)
}

func runCapacity(cmd *cobra.Command, args []string) error {
	c := clusterreg.Capacity{Operator: capacityFlags.operator, NodeID: capacityFlags.id, Bytes: capacityFlags.bytes}
	if err := clusterreg.ValidateCapacity(c); err != nil {
		return clierr.Usage("%v", err)
	}
	in := clusterreg.Direct{
		TypeURL: clusterreg.DeclareCapacityTypeURL, Msg: clusterreg.EncodeCapacity(c),
		FeeAmount: capacityFlags.fee, Gas: capacityFlags.gas, ChainID: capacityFlags.chainID,
		AccountNumber: capacityFlags.account, Sequence: capacityFlags.sequence,
	}
	return SubmitDirect(cmd, capacityFlags.operator, capacityFlags.node, capacityFlags.pubKey, capacityFlags.account, capacityFlags.sequence, in, "declared "+capacityFlags.id)
}

func runRetireNode(cmd *cobra.Command, args []string) error {
	r := clusterreg.Retire{Operator: capacityFlags.operator, ID: capacityFlags.id}
	if err := clusterreg.ValidateRetire(r); err != nil {
		return clierr.Usage("%v", err)
	}
	in := clusterreg.Direct{
		TypeURL: clusterreg.RetireNodeTypeURL, Msg: clusterreg.EncodeRetire(r),
		FeeAmount: capacityFlags.fee, Gas: capacityFlags.gas, ChainID: capacityFlags.chainID,
		AccountNumber: capacityFlags.account, Sequence: capacityFlags.sequence,
	}
	return SubmitDirect(cmd, capacityFlags.operator, capacityFlags.node, capacityFlags.pubKey, capacityFlags.account, capacityFlags.sequence, in, "retired "+capacityFlags.id)
}
