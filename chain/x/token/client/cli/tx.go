package cli

import (
	"fmt"

	"cosmossdk.io/math"
	"github.com/spf13/cobra"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/client/tx"

	"github.com/DeBrosOfficial/network/chain/x/token/types"
)

// GetTxCmd returns the parent `token` transaction command.
func GetTxCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                        types.ModuleName,
		Short:                      "Token factory transaction subcommands",
		DisableFlagParsing:         true,
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}
	cmd.AddCommand(
		GetCmdCreate(),
		GetCmdMint(),
		GetCmdBurn(),
		GetCmdTransfer(),
		GetCmdSetFrozen(),
		GetCmdSetPaused(),
		GetCmdRenounce(),
		GetCmdSetShieldable(),
		GetCmdDelete(),
	)
	return cmd
}

// GetCmdCreate implements `oramad tx token create`.
func GetCmdCreate() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create [subdenom] [name] [symbol]",
		Short: "Create a factory token. The creation fee is burned.",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientTxContext(cmd)
			if err != nil {
				return fmt.Errorf("failed to get client context: %w", err)
			}
			description, err := cmd.Flags().GetString("description")
			if err != nil {
				return err
			}
			mint, err := cmd.Flags().GetBool("mint")
			if err != nil {
				return err
			}
			freeze, err := cmd.Flags().GetBool("freeze")
			if err != nil {
				return err
			}
			delegate, err := cmd.Flags().GetString("permanent-delegate")
			if err != nil {
				return err
			}
			bps, err := cmd.Flags().GetUint32("transfer-fee-bps")
			if err != nil {
				return err
			}
			nonTransferable, err := cmd.Flags().GetBool("non-transferable")
			if err != nil {
				return err
			}
			pause, err := cmd.Flags().GetBool("pause")
			if err != nil {
				return err
			}
			hook, err := cmd.Flags().GetString("transfer-hook")
			if err != nil {
				return err
			}
			msg := &types.MsgCreateToken{
				Creator:           clientCtx.GetFromAddress().String(),
				Subdenom:          args[0],
				Name:              args[1],
				Symbol:            args[2],
				Description:       description,
				Mint:              mint,
				Freeze:            freeze,
				PermanentDelegate: delegate,
				TransferFeeBps:    bps,
				NonTransferable:   nonTransferable,
				Pause:             pause,
				TransferHook:      hook,
			}
			return tx.GenerateOrBroadcastTxCLI(clientCtx, cmd.Flags(), msg)
		},
	}
	cmd.Flags().String("description", "", "token description")
	cmd.Flags().Bool("mint", false, "keep mint authority (renounce-only)")
	cmd.Flags().Bool("freeze", false, "keep freeze authority (renounce-only)")
	cmd.Flags().String("permanent-delegate", "", "permanent delegate address (renounce-only)")
	cmd.Flags().Uint32("transfer-fee-bps", 0, "transfer fee in basis points of the token, burned")
	cmd.Flags().Bool("non-transferable", false, "block transfers until this capability is renounced")
	cmd.Flags().Bool("pause", false, "keep pause authority (renounce-only)")
	cmd.Flags().String("transfer-hook", "", "address of a contract the chain calls on every transfer, gas-capped at 100000 (renounce-only)")
	flags.AddTxFlagsToCmd(cmd)
	return cmd
}

// GetCmdMint implements `oramad tx token mint`.
func GetCmdMint() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mint [denom] [recipient] [amount]",
		Short: "Mint tokens. Requires the mint capability.",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientTxContext(cmd)
			if err != nil {
				return fmt.Errorf("failed to get client context: %w", err)
			}
			amount, err := parseAmount(args[2])
			if err != nil {
				return err
			}
			msg := &types.MsgMint{
				Sender:    clientCtx.GetFromAddress().String(),
				Denom:     args[0],
				Recipient: args[1],
				Amount:    amount,
			}
			return tx.GenerateOrBroadcastTxCLI(clientCtx, cmd.Flags(), msg)
		},
	}
	flags.AddTxFlagsToCmd(cmd)
	return cmd
}

// GetCmdBurn implements `oramad tx token burn`.
func GetCmdBurn() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "burn [denom] [amount]",
		Short: "Burn tokens from the signer's balance",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientTxContext(cmd)
			if err != nil {
				return fmt.Errorf("failed to get client context: %w", err)
			}
			amount, err := parseAmount(args[1])
			if err != nil {
				return err
			}
			msg := &types.MsgBurn{
				Sender: clientCtx.GetFromAddress().String(),
				Denom:  args[0],
				Amount: amount,
			}
			return tx.GenerateOrBroadcastTxCLI(clientCtx, cmd.Flags(), msg)
		},
	}
	flags.AddTxFlagsToCmd(cmd)
	return cmd
}

// GetCmdTransfer implements `oramad tx token transfer`.
func GetCmdTransfer() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "transfer [denom] [from] [to] [amount]",
		Short: "Transfer tokens after freeze, pause, non-transferable, and fee checks",
		Args:  cobra.ExactArgs(4),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientTxContext(cmd)
			if err != nil {
				return fmt.Errorf("failed to get client context: %w", err)
			}
			amount, err := parseAmount(args[3])
			if err != nil {
				return err
			}
			msg := &types.MsgTransfer{
				Sender: clientCtx.GetFromAddress().String(),
				Denom:  args[0],
				From:   args[1],
				To:     args[2],
				Amount: amount,
			}
			return tx.GenerateOrBroadcastTxCLI(clientCtx, cmd.Flags(), msg)
		},
	}
	flags.AddTxFlagsToCmd(cmd)
	return cmd
}

// GetCmdSetFrozen implements `oramad tx token freeze`.
func GetCmdSetFrozen() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "freeze [denom] [account]",
		Short: "Freeze or unfreeze an account. Requires the freeze capability.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientTxContext(cmd)
			if err != nil {
				return fmt.Errorf("failed to get client context: %w", err)
			}
			unfreeze, err := cmd.Flags().GetBool("unfreeze")
			if err != nil {
				return err
			}
			msg := &types.MsgSetFrozen{
				Sender:  clientCtx.GetFromAddress().String(),
				Denom:   args[0],
				Account: args[1],
				Frozen:  !unfreeze,
			}
			return tx.GenerateOrBroadcastTxCLI(clientCtx, cmd.Flags(), msg)
		},
	}
	cmd.Flags().Bool("unfreeze", false, "unfreeze the account instead")
	flags.AddTxFlagsToCmd(cmd)
	return cmd
}

// GetCmdSetPaused implements `oramad tx token pause`.
func GetCmdSetPaused() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pause [denom]",
		Short: "Pause or unpause transfers. Requires the pause capability.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientTxContext(cmd)
			if err != nil {
				return fmt.Errorf("failed to get client context: %w", err)
			}
			unpause, err := cmd.Flags().GetBool("unpause")
			if err != nil {
				return err
			}
			msg := &types.MsgSetPaused{
				Sender: clientCtx.GetFromAddress().String(),
				Denom:  args[0],
				Paused: !unpause,
			}
			return tx.GenerateOrBroadcastTxCLI(clientCtx, cmd.Flags(), msg)
		},
	}
	cmd.Flags().Bool("unpause", false, "unpause transfers instead")
	flags.AddTxFlagsToCmd(cmd)
	return cmd
}

// GetCmdRenounce implements `oramad tx token renounce`.
func GetCmdRenounce() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "renounce [denom] [extension]",
		Short: "Renounce one capability: mint, freeze, delegate, fee, non-transferable, pause, hook",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientTxContext(cmd)
			if err != nil {
				return fmt.Errorf("failed to get client context: %w", err)
			}
			ext, err := parseExtension(args[1])
			if err != nil {
				return err
			}
			msg := &types.MsgRenounce{
				Sender:    clientCtx.GetFromAddress().String(),
				Denom:     args[0],
				Extension: ext,
			}
			return tx.GenerateOrBroadcastTxCLI(clientCtx, cmd.Flags(), msg)
		},
	}
	flags.AddTxFlagsToCmd(cmd)
	return cmd
}

// GetCmdSetShieldable implements `oramad tx token set-shieldable`.
func GetCmdSetShieldable() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set-shieldable [denom]",
		Short: "Mark a token shieldable. Refused while freeze, delegate, or pause powers remain.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientTxContext(cmd)
			if err != nil {
				return fmt.Errorf("failed to get client context: %w", err)
			}
			msg := &types.MsgSetShieldable{
				Sender: clientCtx.GetFromAddress().String(),
				Denom:  args[0],
			}
			return tx.GenerateOrBroadcastTxCLI(clientCtx, cmd.Flags(), msg)
		},
	}
	flags.AddTxFlagsToCmd(cmd)
	return cmd
}

// GetCmdDelete implements `oramad tx token delete`.
func GetCmdDelete() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete [denom]",
		Short: "Delete a zero-supply token and release its metadata deposit",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientTxContext(cmd)
			if err != nil {
				return fmt.Errorf("failed to get client context: %w", err)
			}
			msg := &types.MsgDeleteToken{
				Sender: clientCtx.GetFromAddress().String(),
				Denom:  args[0],
			}
			return tx.GenerateOrBroadcastTxCLI(clientCtx, cmd.Flags(), msg)
		},
	}
	flags.AddTxFlagsToCmd(cmd)
	return cmd
}

func parseAmount(s string) (math.Int, error) {
	amount, ok := math.NewIntFromString(s)
	if !ok {
		return math.Int{}, fmt.Errorf("invalid amount %q", s)
	}
	return amount, nil
}

func parseExtension(s string) (types.Extension, error) {
	switch s {
	case "mint":
		return types.EXTENSION_MINT, nil
	case "freeze":
		return types.EXTENSION_FREEZE, nil
	case "delegate":
		return types.EXTENSION_PERMANENT_DELEGATE, nil
	case "fee":
		return types.EXTENSION_TRANSFER_FEE, nil
	case "non-transferable":
		return types.EXTENSION_NON_TRANSFERABLE, nil
	case "pause":
		return types.EXTENSION_PAUSE, nil
	case "hook":
		return types.EXTENSION_TRANSFER_HOOK, nil
	default:
		return types.EXTENSION_UNSPECIFIED, fmt.Errorf("unknown extension %q", s)
	}
}
