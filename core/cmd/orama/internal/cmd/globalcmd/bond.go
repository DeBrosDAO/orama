package globalcmd

import (
	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/clusterreg"
	"github.com/spf13/cobra"
)

var bondFlags struct {
	chainID  string
	operator string
	id       string
	role     string
	amount   string
	pubKey   string
	account  uint64
	sequence uint64
	fee      string
	gas      uint64
	node     string
}

func newBondCmd(use, short, long, typeURL, verb string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Long:  long,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			roles, err := parseRoles([]string{bondFlags.role})
			if err != nil {
				return err
			}
			if len(roles) != 1 {
				return clierr.Usage("one role is required")
			}
			bond := clusterreg.Bond{
				Operator: bondFlags.operator, NodeID: bondFlags.id, Role: roles[0], Amount: bondFlags.amount,
			}
			if err := clusterreg.ValidateBond(bond); err != nil {
				return clierr.Usage("%v", err)
			}
			in := clusterreg.Direct{
				TypeURL: typeURL, Msg: clusterreg.EncodeBond(bond),
				FeeAmount: bondFlags.fee, Gas: bondFlags.gas, ChainID: bondFlags.chainID,
				AccountNumber: bondFlags.account, Sequence: bondFlags.sequence,
			}
			return SubmitDirect(cmd, bondFlags.operator, bondFlags.node, bondFlags.pubKey, bondFlags.account, bondFlags.sequence, in, verb+" "+bondFlags.id)
		},
	}
	f := cmd.Flags()
	f.StringVar(&bondFlags.chainID, "chain-id", "", "Chain id [required]")
	f.StringVar(&bondFlags.operator, "operator", "", "Operator account (orama1...) [required]")
	f.StringVar(&bondFlags.id, "id", "", "Node id [required]")
	f.StringVar(&bondFlags.role, "role", "", "Role: validator, storage, relay, exit, dirauth, archiver [required]")
	f.StringVar(&bondFlags.amount, "amount", "", "Amount of norama [required]")
	f.StringVar(&bondFlags.pubKey, "pubkey", "", "Compressed secp256k1 pubkey hex of the signing account")
	f.Uint64Var(&bondFlags.account, "account-number", 0, "Account number, when not read from --node")
	f.Uint64Var(&bondFlags.sequence, "sequence", 0, "Account sequence, when not read from --node")
	f.StringVar(&bondFlags.fee, "fee", "", "Fee in norama [required]")
	f.Uint64Var(&bondFlags.gas, "gas", 0, "Gas limit [required]")
	f.StringVar(&bondFlags.node, "node", "", "Chain REST API, for example http://127.0.0.1:31003")
	AddOnionFlags(f)
	return cmd
}

func init() {
	Cmd.AddCommand(newBondCmd("bond", "Bond norama to one role on a global node",
		`Move norama from the operator account into the node's role bond.

The amount is added to the bond that role already holds. The node must already
be registered with that role. Without --node the command prints the sign
document and does not submit it.`,
		clusterreg.BondNodeTypeURL, "bonded"))
	Cmd.AddCommand(newBondCmd("unbond", "Start unbonding norama from one role",
		`Start unbonding norama from one role on a registered global node.

The amount has to be covered by that role's bond. Without --node the command
prints the sign document and does not submit it.`,
		clusterreg.UnbondNodeTypeURL, "unbonded"))
}
