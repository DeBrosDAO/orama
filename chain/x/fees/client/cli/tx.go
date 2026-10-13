package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/client/tx"

	"github.com/DeBrosOfficial/network/chain/x/fees/types"
)

// GetTxCmd returns the parent `fees` transaction command.
func GetTxCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                        types.ModuleName,
		Short:                      "Fees transaction subcommands",
		DisableFlagParsing:         true,
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}
	cmd.AddCommand(GetCmdWithdrawEarnings())
	return cmd
}

// GetCmdWithdrawEarnings implements `oramad tx fees withdraw-earnings`. The signer (--from) owns
// the earnings and receives the withdrawal.
func GetCmdWithdrawEarnings() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "withdraw-earnings [amount-norama]",
		Short: "Move your earnings to your own bank balance",
		Long: "Moves the amount, in norama, from the signer's earnings account to the signer's own " +
			"bank balance, where an ordinary send can spend it. The destination is always the signer " +
			"and cannot be chosen.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			amount, ok := math.NewIntFromString(args[0])
			if !ok {
				return fmt.Errorf("amount %q is not an integer number of norama", args[0])
			}
			clientCtx, err := client.GetClientTxContext(cmd)
			if err != nil {
				return fmt.Errorf("failed to get client context: %w", err)
			}
			msg := &types.MsgWithdrawEarnings{Signer: clientCtx.GetFromAddress().String(), Amount: amount}
			if err := msg.ValidateBasic(); err != nil {
				return err
			}
			return tx.GenerateOrBroadcastTxCLI(clientCtx, cmd.Flags(), msg)
		},
	}
	flags.AddTxFlagsToCmd(cmd)
	return cmd
}
