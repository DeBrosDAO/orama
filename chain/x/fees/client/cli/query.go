// Package cli implements x/fees's read-only CLI query commands (`oramad query fees ...`). x/fees
// ships no Msg service, so there is no tx.go in this package.
package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"

	"github.com/DeBrosOfficial/network/chain/x/fees/types"
)

// GetQueryCmd returns the parent `fees` query command.
func GetQueryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                        types.ModuleName,
		Short:                      "Querying commands for the fees module",
		DisableFlagParsing:         true,
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}

	cmd.AddCommand(
		GetCmdQueryParams(),
		GetCmdQueryBaseFee(),
		GetCmdQueryEarnings(),
		GetCmdQueryDeposit(),
		GetCmdQueryInvariants(),
	)

	return cmd
}

// GetCmdQueryParams implements `oramad query fees params`.
func GetCmdQueryParams() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "params",
		Short: "Query x/fees's genesis-only parameters",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return fmt.Errorf("failed to get client context: %w", err)
			}
			queryClient := types.NewQueryClient(clientCtx)
			res, err := queryClient.Params(cmd.Context(), &types.QueryParamsRequest{})
			if err != nil {
				return fmt.Errorf("failed to query fees params: %w", err)
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

// GetCmdQueryBaseFee implements `oramad query fees base-fee`.
func GetCmdQueryBaseFee() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "base-fee",
		Short: "Query x/fees's current per-gas-unit base fee",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return fmt.Errorf("failed to get client context: %w", err)
			}
			queryClient := types.NewQueryClient(clientCtx)
			res, err := queryClient.BaseFee(cmd.Context(), &types.QueryBaseFeeRequest{})
			if err != nil {
				return fmt.Errorf("failed to query the base fee: %w", err)
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

// GetCmdQueryEarnings implements `oramad query fees earnings [address]`.
func GetCmdQueryEarnings() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "earnings [address]",
		Short: "Query an address's earnings account balance",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return fmt.Errorf("failed to get client context: %w", err)
			}
			queryClient := types.NewQueryClient(clientCtx)
			res, err := queryClient.Earnings(cmd.Context(), &types.QueryEarningsRequest{Address: args[0]})
			if err != nil {
				return fmt.Errorf("failed to query earnings for %s: %w", args[0], err)
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

// GetCmdQueryDeposit implements `oramad query fees deposit [id]`.
func GetCmdQueryDeposit() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deposit [id]",
		Short: "Query an open state deposit by id",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return fmt.Errorf("failed to get client context: %w", err)
			}
			queryClient := types.NewQueryClient(clientCtx)
			res, err := queryClient.Deposit(cmd.Context(), &types.QueryDepositRequest{Id: args[0]})
			if err != nil {
				return fmt.Errorf("failed to query deposit %s: %w", args[0], err)
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

// GetCmdQueryInvariants implements `oramad query fees invariants`.
func GetCmdQueryInvariants() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "invariants",
		Short: "Query x/fees's earnings, deposit and fee-accounting invariants",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return fmt.Errorf("failed to get client context: %w", err)
			}
			queryClient := types.NewQueryClient(clientCtx)
			res, err := queryClient.Invariants(cmd.Context(), &types.QueryInvariantsRequest{})
			if err != nil {
				return fmt.Errorf("failed to query fees invariants: %w", err)
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}
