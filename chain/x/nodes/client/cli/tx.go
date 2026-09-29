package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/client/tx"

	"github.com/DeBrosOfficial/network/chain/x/nodes/types"
)

// GetTxCmd returns the parent `nodes` transaction command.
func GetTxCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                        types.ModuleName,
		Short:                      "Nodes transaction subcommands",
		DisableFlagParsing:         true,
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}
	cmd.AddCommand(GetCmdFundHotKey())
	return cmd
}

// GetCmdFundHotKey implements `oramad tx nodes fund-hot-key`. The signer (--from) is the operator;
// the target is always the registered hot key of the named node.
func GetCmdFundHotKey() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "fund-hot-key [node-id] [amount-norama]",
		Short: "Move earnings to the fee balance of your own node's hot key",
		Long: "Moves the amount, in norama, from the signer's earnings account to the earnings (fee) " +
			"balance of the hot key registered on the signer's own node. The destination is always " +
			"that node's hot key and cannot be chosen.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			amount, ok := math.NewIntFromString(args[1])
			if !ok {
				return fmt.Errorf("amount %q is not an integer number of norama", args[1])
			}
			clientCtx, err := client.GetClientTxContext(cmd)
			if err != nil {
				return fmt.Errorf("failed to get client context: %w", err)
			}
			msg := &types.MsgFundHotKey{
				Operator: clientCtx.GetFromAddress().String(),
				NodeId:   args[0],
				Amount:   amount,
			}
			if err := msg.ValidateBasic(); err != nil {
				return err
			}
			return tx.GenerateOrBroadcastTxCLI(clientCtx, cmd.Flags(), msg)
		},
	}
	flags.AddTxFlagsToCmd(cmd)
	return cmd
}
