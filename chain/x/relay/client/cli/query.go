// Package cli implements x/relay's read-only CLI query commands
// (`oramad query relay ...`).
package cli

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"

	"github.com/DeBrosOfficial/network/chain/x/relay/types"
)

// GetQueryCmd returns the parent `relay` query command.
func GetQueryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                        types.ModuleName,
		Short:                      "Querying commands for the relay module",
		DisableFlagParsing:         true,
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}
	cmd.AddCommand(
		GetCmdQueryParams(),
		GetCmdQueryReporters(),
		GetCmdQueryRelay(),
		GetCmdQueryOperatorRelays(),
		GetCmdQueryRelayPayouts(),
		GetCmdQueryEpochResult(),
		GetCmdQueryInvariants(),
	)
	return cmd
}

// GetCmdQueryParams implements `oramad query relay params`.
func GetCmdQueryParams() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "params",
		Short: "Query x/relay's genesis-only parameters",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return fmt.Errorf("failed to get client context: %w", err)
			}
			res, err := types.NewQueryClient(clientCtx).Params(cmd.Context(), &types.QueryParamsRequest{})
			if err != nil {
				return fmt.Errorf("failed to query relay params: %w", err)
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

// GetCmdQueryReporters implements `oramad query relay reporters`.
func GetCmdQueryReporters() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reporters",
		Short: "Query the relay reporter set",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return fmt.Errorf("failed to get client context: %w", err)
			}
			res, err := types.NewQueryClient(clientCtx).Reporters(cmd.Context(), &types.QueryReportersRequest{})
			if err != nil {
				return fmt.Errorf("failed to query relay reporters: %w", err)
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

// GetCmdQueryRelay implements `oramad query relay relay [fingerprint-hex]`.
func GetCmdQueryRelay() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "relay [fingerprint-hex]",
		Short: "Query one registered relay by its RSA fingerprint hex",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return fmt.Errorf("failed to get client context: %w", err)
			}
			res, err := types.NewQueryClient(clientCtx).Relay(cmd.Context(), &types.QueryRelayRequest{RsaFingerprintHex: args[0]})
			if err != nil {
				return fmt.Errorf("failed to query relay: %w", err)
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

// GetCmdQueryOperatorRelays implements `oramad query relay operator-relays [operator]`.
func GetCmdQueryOperatorRelays() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "operator-relays [operator]",
		Short: "Query an operator's registered relays with the last payout of each",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return fmt.Errorf("failed to get client context: %w", err)
			}
			res, err := types.NewQueryClient(clientCtx).OperatorRelays(cmd.Context(), &types.QueryOperatorRelaysRequest{Operator: args[0]})
			if err != nil {
				return fmt.Errorf("failed to query the relays of %s: %w", args[0], err)
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

// GetCmdQueryRelayPayouts implements `oramad query relay payouts [epoch]`.
func GetCmdQueryRelayPayouts() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "payouts [epoch]",
		Short: "Query what one settled epoch paid each relay",
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
			res, err := types.NewQueryClient(clientCtx).RelayPayouts(cmd.Context(), &types.QueryRelayPayoutsRequest{Epoch: epoch})
			if err != nil {
				return fmt.Errorf("failed to query the relay payouts of epoch %d: %w", epoch, err)
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

// GetCmdQueryEpochResult implements `oramad query relay epoch [epoch]`.
func GetCmdQueryEpochResult() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "epoch [epoch]",
		Short: "Query one epoch's relay settlement",
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
			res, err := types.NewQueryClient(clientCtx).EpochResult(cmd.Context(), &types.QueryEpochResultRequest{Epoch: epoch})
			if err != nil {
				return fmt.Errorf("failed to query relay epoch: %w", err)
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

// GetCmdQueryInvariants implements `oramad query relay invariants`.
func GetCmdQueryInvariants() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "invariants",
		Short: "Query whether relay mints stayed within their ceilings",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return fmt.Errorf("failed to get client context: %w", err)
			}
			res, err := types.NewQueryClient(clientCtx).Invariants(cmd.Context(), &types.QueryInvariantsRequest{})
			if err != nil {
				return fmt.Errorf("failed to query relay invariants: %w", err)
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}
