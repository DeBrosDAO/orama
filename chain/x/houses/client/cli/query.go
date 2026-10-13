// Package cli implements x/houses' read-only query commands.
package cli

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"

	"github.com/DeBrosOfficial/network/chain/x/houses/types"
)

// GetQueryCmd returns the parent `houses` query command.
func GetQueryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                        types.ModuleName,
		Short:                      "Querying commands for the houses module",
		DisableFlagParsing:         true,
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}
	cmd.AddCommand(
		GetCmdQueryParams(),
		GetCmdQueryProposal(),
		GetCmdQueryTiers(),
		GetCmdQueryInvariants(),
	)
	return cmd
}

// GetCmdQueryParams implements `oramad query houses params`.
func GetCmdQueryParams() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "params",
		Short: "Query x/houses parameters",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return fmt.Errorf("failed to get client context: %w", err)
			}
			res, err := types.NewQueryClient(clientCtx).Params(cmd.Context(), &types.QueryParamsRequest{})
			if err != nil {
				return fmt.Errorf("failed to query houses params: %w", err)
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

// GetCmdQueryProposal implements `oramad query houses proposal [id]`.
func GetCmdQueryProposal() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "proposal [id]",
		Short: "Query one proposal",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.ParseUint(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("proposal id: %w", err)
			}
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return fmt.Errorf("failed to get client context: %w", err)
			}
			res, err := types.NewQueryClient(clientCtx).Proposal(cmd.Context(), &types.QueryProposalRequest{ProposalId: id})
			if err != nil {
				return fmt.Errorf("failed to query proposal %d: %w", id, err)
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

// GetCmdQueryTiers implements `oramad query houses tiers`.
func GetCmdQueryTiers() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tiers",
		Short: "Query whether the parameter and structural tiers are open",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return fmt.Errorf("failed to get client context: %w", err)
			}
			res, err := types.NewQueryClient(clientCtx).Tiers(cmd.Context(), &types.QueryTiersRequest{})
			if err != nil {
				return fmt.Errorf("failed to query houses tiers: %w", err)
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

// GetCmdQueryInvariants implements `oramad query houses invariants`.
func GetCmdQueryInvariants() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "invariants",
		Short: "Query the house-bond invariant",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return fmt.Errorf("failed to get client context: %w", err)
			}
			res, err := types.NewQueryClient(clientCtx).Invariants(cmd.Context(), &types.QueryInvariantsRequest{})
			if err != nil {
				return fmt.Errorf("failed to query houses invariants: %w", err)
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}
