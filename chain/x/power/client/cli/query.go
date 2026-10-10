// Package cli implements x/power's CLI commands: read-only queries (`oramad query power ...`) and
// a genesis-only bootstrap committee builder (`oramad genesis add-bootstrap-validator`). x/power
// ships no Msg service, so there is no tx.go in this package.
package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"

	"github.com/DeBrosOfficial/network/chain/x/power/types"
)

// GetQueryCmd returns the parent `power` query command.
func GetQueryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                        types.ModuleName,
		Short:                      "Querying commands for the power module",
		DisableFlagParsing:         true,
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}

	cmd.AddCommand(
		GetCmdQueryParams(),
		GetCmdQueryBootstrapCommittee(),
		GetCmdQueryLambda(),
		GetCmdQueryValidatorPower(),
		GetCmdQueryInvariants(),
	)

	return cmd
}

// GetCmdQueryParams implements `oramad query power params`.
func GetCmdQueryParams() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "params",
		Short: "Query x/power's genesis-only parameters",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return fmt.Errorf("failed to get client context: %w", err)
			}
			queryClient := types.NewQueryClient(clientCtx)
			res, err := queryClient.Params(cmd.Context(), &types.QueryParamsRequest{})
			if err != nil {
				return fmt.Errorf("failed to query power params: %w", err)
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

// GetCmdQueryBootstrapCommittee implements `oramad query power bootstrap-committee`.
func GetCmdQueryBootstrapCommittee() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bootstrap-committee",
		Short: "Query x/power's fixed genesis bootstrap committee",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return fmt.Errorf("failed to get client context: %w", err)
			}
			queryClient := types.NewQueryClient(clientCtx)
			res, err := queryClient.BootstrapCommittee(cmd.Context(), &types.QueryBootstrapCommitteeRequest{})
			if err != nil {
				return fmt.Errorf("failed to query the bootstrap committee: %w", err)
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

// GetCmdQueryLambda implements `oramad query power lambda`.
func GetCmdQueryLambda() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "lambda",
		Short: "Query x/power's current hand-over factor lambda and cap state",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return fmt.Errorf("failed to get client context: %w", err)
			}
			queryClient := types.NewQueryClient(clientCtx)
			res, err := queryClient.Lambda(cmd.Context(), &types.QueryLambdaRequest{})
			if err != nil {
				return fmt.Errorf("failed to query lambda: %w", err)
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

// GetCmdQueryValidatorPower implements `oramad query power validator-power [operator-address]`.
func GetCmdQueryValidatorPower() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validator-power [operator-address]",
		Short: "Query the last CometBFT power x/power assigned a validator",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return fmt.Errorf("failed to get client context: %w", err)
			}
			queryClient := types.NewQueryClient(clientCtx)
			res, err := queryClient.ValidatorPower(cmd.Context(), &types.QueryValidatorPowerRequest{OperatorAddress: args[0]})
			if err != nil {
				return fmt.Errorf("failed to query validator power for %s: %w", args[0], err)
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

// GetCmdQueryInvariants implements `oramad query power invariants`.
func GetCmdQueryInvariants() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "invariants",
		Short: "Query the module-account invariant",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return fmt.Errorf("failed to get client context: %w", err)
			}
			res, err := types.NewQueryClient(clientCtx).Invariants(cmd.Context(), &types.QueryInvariantsRequest{})
			if err != nil {
				return fmt.Errorf("failed to query power invariants: %w", err)
			}
			return clientCtx.PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}
