// Package cli implements x/emission's read-only CLI query commands
// (`oramad query emission ...`). x/emission's only Msg is the test-network faucet, sent as a generic tx, so there is no tx.go in
// this package.
package cli

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"

	"github.com/DeBrosOfficial/network/chain/x/emission/types"
)

// GetQueryCmd returns the parent `emission` query command.
func GetQueryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                        types.ModuleName,
		Short:                      "Querying commands for the emission module",
		DisableFlagParsing:         true,
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}

	cmd.AddCommand(
		GetCmdQueryParams(),
		GetCmdQueryCurrentEpoch(),
		GetCmdQueryScheduleAt(),
		GetCmdQueryCumulativeMinted(),
		GetCmdQuerySupplyCapSoFar(),
		GetCmdQueryInvariants(),
	)

	return cmd
}

// GetCmdQueryParams implements `oramad query emission params`.
func GetCmdQueryParams() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "params",
		Short: "Query x/emission's genesis-only, immutable parameters",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return fmt.Errorf("failed to get client context: %w", err)
			}
			queryClient := types.NewQueryClient(clientCtx)
			res, err := queryClient.Params(cmd.Context(), &types.QueryParamsRequest{})
			if err != nil {
				return fmt.Errorf("failed to query emission params: %w", err)
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

// GetCmdQueryCurrentEpoch implements `oramad query emission current-epoch`.
func GetCmdQueryCurrentEpoch() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "current-epoch",
		Short: "Query x/emission's current epoch state",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return fmt.Errorf("failed to get client context: %w", err)
			}
			queryClient := types.NewQueryClient(clientCtx)
			res, err := queryClient.CurrentEpoch(cmd.Context(), &types.QueryCurrentEpochRequest{})
			if err != nil {
				return fmt.Errorf("failed to query the current emission epoch: %w", err)
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

// GetCmdQueryScheduleAt implements `oramad query emission schedule-at [epoch]`.
func GetCmdQueryScheduleAt() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "schedule-at [epoch]",
		Short: "Query the emission schedule's maximum mint and split at the given epoch",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			epoch, err := strconv.ParseUint(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid epoch %q: %w", args[0], err)
			}
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return fmt.Errorf("failed to get client context: %w", err)
			}
			queryClient := types.NewQueryClient(clientCtx)
			res, err := queryClient.ScheduleAt(cmd.Context(), &types.QueryScheduleAtRequest{Epoch: epoch})
			if err != nil {
				return fmt.Errorf("failed to query the emission schedule at epoch %d: %w", epoch, err)
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

// GetCmdQueryCumulativeMinted implements `oramad query emission cumulative-minted`.
func GetCmdQueryCumulativeMinted() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cumulative-minted",
		Short: "Query x/emission's all-time cumulative minted and burned norama",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return fmt.Errorf("failed to get client context: %w", err)
			}
			queryClient := types.NewQueryClient(clientCtx)
			res, err := queryClient.CumulativeMinted(cmd.Context(), &types.QueryCumulativeMintedRequest{})
			if err != nil {
				return fmt.Errorf("failed to query cumulative emission minted: %w", err)
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

// GetCmdQuerySupplyCapSoFar implements `oramad query emission supply-cap-so-far [epoch]`.
func GetCmdQuerySupplyCapSoFar() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "supply-cap-so-far [epoch]",
		Short: "Query the schedule's maximum cumulative supply through the given epoch",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			epoch, err := strconv.ParseUint(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid epoch %q: %w", args[0], err)
			}
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return fmt.Errorf("failed to get client context: %w", err)
			}
			queryClient := types.NewQueryClient(clientCtx)
			res, err := queryClient.SupplyCapSoFar(cmd.Context(), &types.QuerySupplyCapSoFarRequest{Epoch: epoch})
			if err != nil {
				return fmt.Errorf("failed to query the emission supply cap through epoch %d: %w", epoch, err)
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

// GetCmdQueryInvariants implements `oramad query emission invariants`.
func GetCmdQueryInvariants() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "invariants",
		Short: "Check x/emission's supply invariants against the current chain state",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return fmt.Errorf("failed to get client context: %w", err)
			}
			queryClient := types.NewQueryClient(clientCtx)
			res, err := queryClient.Invariants(cmd.Context(), &types.QueryInvariantsRequest{})
			if err != nil {
				return fmt.Errorf("failed to query emission invariants: %w", err)
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}
