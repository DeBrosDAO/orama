// Package cli implements x/token's CLI commands. The module is not registered
// in the app, so oramad does not mount these commands.
package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"

	"github.com/DeBrosOfficial/network/chain/x/token/types"
)

// GetQueryCmd returns the parent `token` query command.
func GetQueryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                        types.ModuleName,
		Short:                      "Querying commands for the token module",
		DisableFlagParsing:         true,
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}
	cmd.AddCommand(
		GetCmdQueryParams(),
		GetCmdQueryToken(),
		GetCmdQueryFrozen(),
		GetCmdQueryInvariants(),
	)
	return cmd
}

// GetCmdQueryParams implements `oramad query token params`.
func GetCmdQueryParams() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "params",
		Short: "Query x/token's genesis-only parameters",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return fmt.Errorf("failed to get client context: %w", err)
			}
			res, err := types.NewQueryClient(clientCtx).Params(cmd.Context(), &types.QueryParamsRequest{})
			if err != nil {
				return fmt.Errorf("failed to query token params: %w", err)
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

// GetCmdQueryToken implements `oramad query token token [denom]`.
func GetCmdQueryToken() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "token [denom]",
		Short: "Query one factory token",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return fmt.Errorf("failed to get client context: %w", err)
			}
			res, err := types.NewQueryClient(clientCtx).Token(cmd.Context(), &types.QueryTokenRequest{Denom: args[0]})
			if err != nil {
				return fmt.Errorf("failed to query token %s: %w", args[0], err)
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

// GetCmdQueryFrozen implements `oramad query token frozen [denom] [account]`.
func GetCmdQueryFrozen() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "frozen [denom] [account]",
		Short: "Query whether an account is frozen for a token",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return fmt.Errorf("failed to get client context: %w", err)
			}
			res, err := types.NewQueryClient(clientCtx).Frozen(cmd.Context(), &types.QueryFrozenRequest{
				Denom:   args[0],
				Account: args[1],
			})
			if err != nil {
				return fmt.Errorf("failed to query freeze of %s on %s: %w", args[1], args[0], err)
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

// GetCmdQueryInvariants implements `oramad query token invariants`.
func GetCmdQueryInvariants() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "invariants",
		Short: "Query x/token's supply and deposit invariants",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return fmt.Errorf("failed to get client context: %w", err)
			}
			res, err := types.NewQueryClient(clientCtx).Invariants(cmd.Context(), &types.QueryInvariantsRequest{})
			if err != nil {
				return fmt.Errorf("failed to query token invariants: %w", err)
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}
