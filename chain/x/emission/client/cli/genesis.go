package cli

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/server"
	genutiltypes "github.com/cosmos/cosmos-sdk/x/genutil/types"

	"github.com/DeBrosOfficial/network/chain/x/emission/types"
)

const (
	flagEpochDuration       = "epoch-duration"
	flagMinBlocksPerEpoch   = "min-blocks-per-epoch"
	flagAllowBootstrapStake = "allow-bootstrap-stake"
)

// SetEmissionParamsCmd returns the `genesis set-emission-params` command. It rewrites
// x/emission's Params directly in genesis.json at --home.
//
// This only makes sense before the chain has started: x/emission ships no Msg service (see
// plans/open-network.md D18, "the emission schedule is ossified"), so once a genesis file has been
// used for the first `oramad start` these parameters can never be changed again short of a
// coordinated hard fork. Running this command against a genesis file after that point edits a
// file the running chain no longer reads from.
func SetEmissionParamsCmd(defaultNodeHome string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set-emission-params",
		Short: "Set x/emission's epoch-duration and min-blocks-per-epoch in genesis.json",
		Long: `Rewrite x/emission's genesis parameters in genesis.json.

Must be run before the chain's first 'oramad start': x/emission ships no Msg service, so once a
chain has produced its first block these parameters are immutable short of a coordinated hard
fork. A devnet or localnet typically shortens both and sets --allow-bootstrap-stake, e.g.
--epoch-duration 60s --min-blocks-per-epoch 5 --allow-bootstrap-stake. Without
--allow-bootstrap-stake, epoch-duration must be at least 24h and min-blocks-per-epoch at least
14,400 (see docs/CHAIN.md).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			epochDurationStr, err := cmd.Flags().GetString(flagEpochDuration)
			if err != nil {
				return fmt.Errorf("failed to read --%s: %w", flagEpochDuration, err)
			}
			minBlocks, err := cmd.Flags().GetUint64(flagMinBlocksPerEpoch)
			if err != nil {
				return fmt.Errorf("failed to read --%s: %w", flagMinBlocksPerEpoch, err)
			}
			allowBootstrapStake, err := cmd.Flags().GetBool(flagAllowBootstrapStake)
			if err != nil {
				return fmt.Errorf("failed to read --%s: %w", flagAllowBootstrapStake, err)
			}
			epochDuration, err := time.ParseDuration(epochDurationStr)
			if err != nil {
				return fmt.Errorf("invalid --%s %q: %w", flagEpochDuration, epochDurationStr, err)
			}

			newParams := types.NewParams(epochDuration, minBlocks, allowBootstrapStake)
			if err := newParams.Validate(); err != nil {
				return fmt.Errorf("invalid emission params: %w", err)
			}

			clientCtx := client.GetClientContextFromCmd(cmd)
			serverCtx := server.GetServerContextFromCmd(cmd)
			cometConfig := serverCtx.Config
			cometConfig.SetRoot(clientCtx.HomeDir)

			genFile := cometConfig.GenesisFile()
			appGenesis, err := genutiltypes.AppGenesisFromFile(genFile)
			if err != nil {
				return fmt.Errorf("failed to read genesis file %s: %w", genFile, err)
			}

			var appState map[string]json.RawMessage
			if err := json.Unmarshal(appGenesis.AppState, &appState); err != nil {
				return fmt.Errorf("failed to unmarshal app state from %s: %w", genFile, err)
			}

			genState := *types.DefaultGenesisState()
			if raw, ok := appState[types.ModuleName]; ok && len(raw) > 0 {
				if err := clientCtx.Codec.UnmarshalJSON(raw, &genState); err != nil {
					return fmt.Errorf("failed to unmarshal existing %s genesis state: %w", types.ModuleName, err)
				}
			}
			genState.Params = newParams
			if err := genState.Validate(); err != nil {
				return fmt.Errorf("resulting %s genesis state is invalid: %w", types.ModuleName, err)
			}

			appState[types.ModuleName] = clientCtx.Codec.MustMarshalJSON(&genState)
			rawAppState, err := json.Marshal(appState)
			if err != nil {
				return fmt.Errorf("failed to marshal app state: %w", err)
			}
			appGenesis.AppState = rawAppState

			if err := appGenesis.SaveAs(genFile); err != nil {
				return fmt.Errorf("failed to save genesis file %s: %w", genFile, err)
			}

			cmd.Printf(
				"emission params set: epoch_duration=%s min_blocks_per_epoch=%d allow_bootstrap_stake=%t\n",
				epochDuration, minBlocks, allowBootstrapStake,
			)
			return nil
		},
	}

	cmd.Flags().String(flagEpochDuration, types.DefaultEpochDuration.String(), "minimum BFT time an epoch must run before it can close (Go duration syntax, e.g. 24h, 60s)")
	cmd.Flags().Uint64(flagMinBlocksPerEpoch, types.DefaultMinBlocksPerEpoch, "minimum number of blocks an epoch must run before it can close")
	cmd.Flags().Bool(flagAllowBootstrapStake, false, "allow a nonzero genesis supply (devnet/stagenet/localnet chain-ids only; also relaxes the epoch-duration/min-blocks-per-epoch floors)")
	cmd.Flags().String(flags.FlagHome, defaultNodeHome, "The application home directory")

	return cmd
}
