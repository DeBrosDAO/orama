// Package cli implements x/nodes CLI commands: queries (`oramad query nodes ...`) and `oramad tx nodes fund-hot-key`.
package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"

	"github.com/DeBrosOfficial/network/chain/x/nodes/types"
)

// GetQueryCmd returns the parent `nodes` query command.
func GetQueryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                        types.ModuleName,
		Short:                      "Querying commands for the nodes module",
		DisableFlagParsing:         true,
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}
	cmd.AddCommand(
		GetCmdQueryParams(),
		GetCmdQueryOperator(),
		GetCmdQueryNode(),
		GetCmdQueryCluster(),
		GetCmdQueryUnbondings(),
		GetCmdQueryInvariants(),
	)
	return cmd
}

// GetCmdQueryParams implements `oramad query nodes params`.
func GetCmdQueryParams() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "params",
		Short: "Query x/nodes' genesis-only parameters",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return fmt.Errorf("failed to get client context: %w", err)
			}
			res, err := types.NewQueryClient(clientCtx).Params(cmd.Context(), &types.QueryParamsRequest{})
			if err != nil {
				return fmt.Errorf("failed to query nodes params: %w", err)
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

// GetCmdQueryOperator implements `oramad query nodes operator [address]`.
func GetCmdQueryOperator() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "operator [address]",
		Short: "Query a registered operator",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return fmt.Errorf("failed to get client context: %w", err)
			}
			res, err := types.NewQueryClient(clientCtx).Operator(cmd.Context(), &types.QueryOperatorRequest{Address: args[0]})
			if err != nil {
				return fmt.Errorf("failed to query operator: %w", err)
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

// GetCmdQueryNode implements `oramad query nodes node [id]`.
func GetCmdQueryNode() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "node [id]",
		Short: "Query a global node",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return fmt.Errorf("failed to get client context: %w", err)
			}
			res, err := types.NewQueryClient(clientCtx).Node(cmd.Context(), &types.QueryNodeRequest{NodeId: args[0]})
			if err != nil {
				return fmt.Errorf("failed to query node: %w", err)
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

// GetCmdQueryCluster implements `oramad query nodes cluster [id]`.
func GetCmdQueryCluster() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cluster [id]",
		Short: "Query an optional cluster registry row",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return fmt.Errorf("failed to get client context: %w", err)
			}
			res, err := types.NewQueryClient(clientCtx).Cluster(cmd.Context(), &types.QueryClusterRequest{ClusterId: args[0]})
			if err != nil {
				return fmt.Errorf("failed to query cluster: %w", err)
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

// GetCmdQueryUnbondings implements `oramad query nodes unbondings [node-id]`.
func GetCmdQueryUnbondings() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "unbondings [node-id]",
		Short: "Query a node's unbonding queue",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return fmt.Errorf("failed to get client context: %w", err)
			}
			res, err := types.NewQueryClient(clientCtx).NodeUnbondings(cmd.Context(), &types.QueryNodeUnbondingsRequest{NodeId: args[0]})
			if err != nil {
				return fmt.Errorf("failed to query unbondings: %w", err)
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

// GetCmdQueryInvariants implements `oramad query nodes invariants`.
func GetCmdQueryInvariants() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "invariants",
		Short: "Query x/nodes invariant checks",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return fmt.Errorf("failed to get client context: %w", err)
			}
			res, err := types.NewQueryClient(clientCtx).Invariants(cmd.Context(), &types.QueryInvariantsRequest{})
			if err != nil {
				return fmt.Errorf("failed to query nodes invariants: %w", err)
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}
