package globalcmd

import (
	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/clusterreg"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// txFlags are the signing flags every validator transaction takes.
type txFlags struct {
	chainID  string
	operator string
	pubKey   string
	account  uint64
	sequence uint64
	fee      string
	gas      uint64
	node     string
}

func (t *txFlags) register(f *pflag.FlagSet) {
	f.StringVar(&t.chainID, "chain-id", "", "Chain id [required]")
	f.StringVar(&t.operator, "operator", "", "Validator operator account (orama1...) [required]")
	f.StringVar(&t.pubKey, "pubkey", "", "Compressed secp256k1 pubkey hex of the signing account")
	f.Uint64Var(&t.account, "account-number", 0, "Account number, when not read from --node")
	f.Uint64Var(&t.sequence, "sequence", 0, "Account sequence, when not read from --node")
	f.StringVar(&t.fee, "fee", "", "Fee in norama [required]")
	f.Uint64Var(&t.gas, "gas", 0, "Gas limit [required]")
	f.StringVar(&t.node, "node", "", "Chain REST API, for example http://127.0.0.1:31003")
}

func (t *txFlags) submit(cmd *cobra.Command, typeURL string, msg []byte, verb string) error {
	in := clusterreg.Direct{
		TypeURL: typeURL, Msg: msg, FeeAmount: t.fee, Gas: t.gas, ChainID: t.chainID,
		AccountNumber: t.account, Sequence: t.sequence,
	}
	return SubmitDirect(cmd, t.operator, t.node, t.pubKey, t.account, t.sequence, in, verb)
}

var unjailFlags txFlags

var unjailCmd = &cobra.Command{
	Use:   "unjail",
	Short: "Build or send MsgUnjail for the operator's validator",
	Long: `Build x/slashing MsgUnjail for the validator whose operator account is
--operator (the same bytes as its oramavaloper address), signed by that account.

x/slashing refuses it while the jail period runs, when the validator has no
self-delegation or less than its minimum, and for a tombstoned validator, which
can never unjail. Without --node the command prints the sign document and does
not submit it; with --node the RootWallet agent signs and it is broadcast.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		validator, err := clusterreg.ValidatorAddress(unjailFlags.operator)
		if err != nil {
			return clierr.Usage("%v", err)
		}
		return unjailFlags.submit(cmd, clusterreg.UnjailTypeURL, clusterreg.EncodeUnjail(validator), "unjailed "+validator)
	},
}

var editFlags struct {
	txFlags
	moniker, identity, website, contact, details, commission string
}

var editCmd = &cobra.Command{
	Use:   "edit",
	Short: "Build or send MsgEditValidator (description, commission)",
	Long: `Build x/staking MsgEditValidator for the operator's validator. Only the flags
given change; every other description field is sent as [do-not-modify]. An empty
value clears that field. --commission-rate is a decimal from 0 to 1; x/staking
allows one commission change per 24 hours, within the validator's
max-change-rate. Without --node the command prints the sign document.`,
	Args: cobra.NoArgs,
	RunE: runEdit,
}

func init() {
	unjailFlags.register(unjailCmd.Flags())
	f := editCmd.Flags()
	editFlags.register(f)
	f.StringVar(&editFlags.moniker, "moniker", "", "New moniker")
	f.StringVar(&editFlags.identity, "identity", "", "New identity (for example a keybase id)")
	f.StringVar(&editFlags.website, "website", "", "New website")
	f.StringVar(&editFlags.contact, "security-contact", "", "New security contact")
	f.StringVar(&editFlags.details, "details", "", "New details")
	f.StringVar(&editFlags.commission, "commission-rate", "", "New commission rate, 0 to 1")
	validatorCmd.AddCommand(unjailCmd, editCmd)
}

func runEdit(cmd *cobra.Command, _ []string) error {
	validator, err := clusterreg.ValidatorAddress(editFlags.operator)
	if err != nil {
		return clierr.Usage("%v", err)
	}
	e := clusterreg.NewValidatorEdit(validator)
	changed := false
	for flag, field := range map[string]*string{
		"moniker": &e.Moniker, "identity": &e.Identity, "website": &e.Website,
		"security-contact": &e.SecurityContact, "details": &e.Details, "commission-rate": &e.CommissionRate,
	} {
		if value, err := cmd.Flags().GetString(flag); err == nil && cmd.Flags().Changed(flag) {
			*field, changed = value, true
		}
	}
	if !changed {
		return clierr.Usage("nothing to change; give at least one of --moniker, --identity, --website, --security-contact, --details, --commission-rate")
	}
	msg, err := clusterreg.EncodeEditValidator(e)
	if err != nil {
		return clierr.Usage("%v", err)
	}
	return editFlags.submit(cmd, clusterreg.EditValidatorTypeURL, msg, "edited "+validator)
}
