// Package cli implements x/archive's query and tx commands.
package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"

	"github.com/DeBrosOfficial/network/chain/x/archive/types"
)

// GetQueryCmd returns the parent `archive` query command.
func GetQueryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                        types.ModuleName,
		Short:                      "Querying commands for the archive module",
		DisableFlagParsing:         true,
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}
	cmd.AddCommand(
		GetCmdQueryParams(),
		GetCmdQueryRange(),
		GetCmdQueryLastArchivedHeight(),
		GetCmdQueryRetainHeight(),
	)
	return cmd
}

// GetCmdQueryParams implements `oramad query archive params`.
func GetCmdQueryParams() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "params",
		Short: "Query x/archive's genesis-only parameters",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return fmt.Errorf("failed to get client context: %w", err)
			}
			res, err := types.NewQueryClient(clientCtx).Params(cmd.Context(), &types.QueryParamsRequest{})
			if err != nil {
				return fmt.Errorf("failed to query archive params: %w", err)
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

// GetCmdQueryRange implements `oramad query archive range [start] [end]`.
func GetCmdQueryRange() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "range [start-height] [end-height]",
		Short: "Query one archived or pending height range",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			start, err := parseHeight(args[0])
			if err != nil {
				return err
			}
			end, err := parseHeight(args[1])
			if err != nil {
				return err
			}
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return fmt.Errorf("failed to get client context: %w", err)
			}
			res, err := types.NewQueryClient(clientCtx).Range(cmd.Context(), &types.QueryRangeRequest{
				StartHeight: start,
				EndHeight:   end,
			})
			if err != nil {
				return fmt.Errorf("failed to query archive range: %w", err)
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

// GetCmdQueryLastArchivedHeight implements `oramad query archive last-archived-height`.
func GetCmdQueryLastArchivedHeight() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "last-archived-height",
		Short: "Query the contiguous archived prefix",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return fmt.Errorf("failed to get client context: %w", err)
			}
			res, err := types.NewQueryClient(clientCtx).LastArchivedHeight(cmd.Context(), &types.QueryLastArchivedHeightRequest{})
			if err != nil {
				return fmt.Errorf("failed to query last archived height: %w", err)
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

// GetCmdQueryRetainHeight implements `oramad query archive retain-height`.
func GetCmdQueryRetainHeight() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "retain-height",
		Short: "Query the retain height at the current tip",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return fmt.Errorf("failed to get client context: %w", err)
			}
			res, err := types.NewQueryClient(clientCtx).RetainHeight(cmd.Context(), &types.QueryRetainHeightRequest{})
			if err != nil {
				return fmt.Errorf("failed to query retain height: %w", err)
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}
