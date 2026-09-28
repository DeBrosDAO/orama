// Package cli implements x/market's read-only CLI query commands.
package cli

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"

	"github.com/DeBrosOfficial/network/chain/x/market/types"
)

// GetQueryCmd returns the parent `market` query command.
func GetQueryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                        types.ModuleName,
		Short:                      "Querying commands for the market module",
		DisableFlagParsing:         true,
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}
	cmd.AddCommand(GetCmdQueryListing(), GetCmdQueryBid())
	return cmd
}

// GetCmdQueryListing implements `oramad query market listing [id]`.
func GetCmdQueryListing() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "listing [id]",
		Short: "Query one listing",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.ParseUint(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("listing id: %w", err)
			}
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return fmt.Errorf("failed to get client context: %w", err)
			}
			res, err := types.NewQueryClient(clientCtx).Listing(cmd.Context(), &types.QueryListingRequest{Id: id})
			if err != nil {
				return fmt.Errorf("failed to query listing: %w", err)
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

// GetCmdQueryBid implements `oramad query market bid [listing-id] [bid-id]`.
func GetCmdQueryBid() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bid [listing-id] [bid-id]",
		Short: "Query one bid",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			listingID, err := strconv.ParseUint(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("listing id: %w", err)
			}
			bidID, err := strconv.ParseUint(args[1], 10, 64)
			if err != nil {
				return fmt.Errorf("bid id: %w", err)
			}
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return fmt.Errorf("failed to get client context: %w", err)
			}
			res, err := types.NewQueryClient(clientCtx).Bid(cmd.Context(), &types.QueryBidRequest{ListingId: listingID, BidId: bidID})
			if err != nil {
				return fmt.Errorf("failed to query bid: %w", err)
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}
