// Package cli implements x/cnft's read-only CLI query commands.
package cli

import (
	"encoding/hex"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"

	"github.com/DeBrosOfficial/network/chain/x/cnft/types"
)

// GetQueryCmd returns the parent `cnft` query command.
func GetQueryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                        types.ModuleName,
		Short:                      "Querying commands for the cnft module",
		DisableFlagParsing:         true,
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}
	cmd.AddCommand(
		GetCmdQueryCollection(),
		GetCmdQueryTree(),
		GetCmdQueryDecompressed(),
		GetCmdQuerySnapshots(),
	)
	return cmd
}

// GetCmdQueryCollection implements `oramad query cnft collection [id]`.
func GetCmdQueryCollection() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "collection [id]",
		Short: "Query one cNFT collection",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.ParseUint(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("collection id: %w", err)
			}
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return fmt.Errorf("failed to get client context: %w", err)
			}
			res, err := types.NewQueryClient(clientCtx).Collection(cmd.Context(), &types.QueryCollectionRequest{Id: id})
			if err != nil {
				return fmt.Errorf("failed to query collection: %w", err)
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

// GetCmdQueryTree implements `oramad query cnft tree [id]`.
func GetCmdQueryTree() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tree [id]",
		Short: "Query one compressed-NFT tree",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.ParseUint(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("tree id: %w", err)
			}
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return fmt.Errorf("failed to get client context: %w", err)
			}
			res, err := types.NewQueryClient(clientCtx).Tree(cmd.Context(), &types.QueryTreeRequest{Id: id})
			if err != nil {
				return fmt.Errorf("failed to query tree: %w", err)
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

// GetCmdQueryDecompressed implements `oramad query cnft decompressed [asset-id-hex]`.
func GetCmdQueryDecompressed() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "decompressed [asset-id-hex]",
		Short: "Query an asset's on-chain decompressed handle",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			assetID, err := hex.DecodeString(args[0])
			if err != nil {
				return fmt.Errorf("asset id: %w", err)
			}
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return fmt.Errorf("failed to get client context: %w", err)
			}
			res, err := types.NewQueryClient(clientCtx).Decompressed(cmd.Context(), &types.QueryDecompressedRequest{AssetId: assetID})
			if err != nil {
				return fmt.Errorf("failed to query decompressed asset: %w", err)
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

// GetCmdQuerySnapshots implements `oramad query cnft snapshots [tree-id]`.
func GetCmdQuerySnapshots() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "snapshots [tree-id]",
		Short: "Query snapshot CIDs recorded for a tree",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.ParseUint(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("tree id: %w", err)
			}
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return fmt.Errorf("failed to get client context: %w", err)
			}
			res, err := types.NewQueryClient(clientCtx).Snapshots(cmd.Context(), &types.QuerySnapshotsRequest{TreeId: id})
			if err != nil {
				return fmt.Errorf("failed to query snapshots: %w", err)
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}
