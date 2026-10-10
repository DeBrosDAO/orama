package cli

import (
	"encoding/json"
	"fmt"
	"time"

	"cosmossdk.io/math"
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
	flagFaucetEnabled       = "faucet-enabled"
	flagFaucetMaxDrip       = "faucet-max-drip"
	flagFaucetEpochCap      = "faucet-epoch-cap"
	flagFaucetCooldown      = "faucet-cooldown"
)

// SetEmissionParamsCmd returns the `genesis set-emission-params` command. It rewrites
// x/emission's Params directly in genesis.json at --home.
//
// This only makes sense before the chain has started: no x/emission Msg writes them (see
// plans/open-network.md D18, "the emission schedule is ossified"), so once a genesis file has been
// used for the first `oramad start` these parameters can never be changed again short of a
// coordinated hard fork. Running this command against a genesis file after that point edits a
// file the running chain no longer reads from.
func SetEmissionParamsCmd(defaultNodeHome string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set-emission-params",
		Short: "Set x/emission's epoch-duration and min-blocks-per-epoch in genesis.json",
		Long: `Rewrite x/emission's genesis parameters in genesis.json.

Must be run before the chain's first 'oramad start': no x/emission Msg writes them, so once a
chain has produced its first block these parameters are immutable short of a coordinated hard
fork. A devnet or localnet typically shortens both and sets --allow-bootstrap-stake, e.g.
--epoch-duration 60s --min-blocks-per-epoch 5 --allow-bootstrap-stake. Without
--allow-bootstrap-stake, epoch-duration must be at least 24h and min-blocks-per-epoch at least
14,400 (see docs/whitepaper/technical-reference/vol2/39-chain-architecture.md).

The faucet flags (--faucet-enabled and friends) set the test-network faucet's parameters; only
the ones passed change the value already in genesis. --faucet-enabled is refused on a production
chain-id (see "Test-network faucet" in docs/whitepaper/technical-reference/appendices/e-chain-messages-and-queries.md).`,
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
			// Keep the faucet params already in genesis: only the faucet flags that were
			// passed change them.
			newParams := genState.Params
			newParams.EpochDurationSeconds = int64(epochDuration.Seconds())
			newParams.MinBlocksPerEpoch = minBlocks
			newParams.AllowBootstrapStake = allowBootstrapStake
			if err := applyFaucetFlags(cmd, &newParams); err != nil {
				return err
			}
			if err := newParams.Validate(); err != nil {
				return fmt.Errorf("invalid emission params: %w", err)
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
				"emission params set: epoch_duration=%s min_blocks_per_epoch=%d allow_bootstrap_stake=%t faucet_enabled=%t\n",
				epochDuration, minBlocks, allowBootstrapStake, newParams.FaucetEnabled,
			)
			return nil
		},
	}

	cmd.Flags().String(flagEpochDuration, types.DefaultEpochDuration.String(), "minimum BFT time an epoch must run before it can close (Go duration syntax, e.g. 24h, 60s)")
	cmd.Flags().Uint64(flagMinBlocksPerEpoch, types.DefaultMinBlocksPerEpoch, "minimum number of blocks an epoch must run before it can close")
	cmd.Flags().Bool(flagAllowBootstrapStake, false, "allow a nonzero genesis supply (devnet/stagenet/localnet chain-ids only; also relaxes the epoch-duration/min-blocks-per-epoch floors)")
	cmd.Flags().Bool(flagFaucetEnabled, false, "enable the test-network faucet (devnet/stagenet/localnet chain-ids only; leaves the genesis value unchanged when not passed)")
	cmd.Flags().String(flagFaucetMaxDrip, types.DefaultFaucetMaxDrip().String(), "largest single faucet drip, in norama")
	cmd.Flags().String(flagFaucetEpochCap, types.DefaultFaucetMaxDrip().MulRaw(types.DefaultFaucetEpochCapDrips).String(), "most norama the faucet may mint per epoch")
	cmd.Flags().Uint64(flagFaucetCooldown, types.DefaultFaucetRecipientCooldown, "seconds between two faucet drips to the same recipient (0 disables)")
	cmd.Flags().String(flags.FlagHome, defaultNodeHome, "The application home directory")

	return cmd
}

// applyFaucetFlags overwrites the faucet params in p with every faucet flag the caller passed;
// flags that were not passed leave p as read from genesis.
func applyFaucetFlags(cmd *cobra.Command, p *types.Params) error {
	if cmd.Flags().Changed(flagFaucetEnabled) {
		enabled, err := cmd.Flags().GetBool(flagFaucetEnabled)
		if err != nil {
			return fmt.Errorf("failed to read --%s: %w", flagFaucetEnabled, err)
		}
		p.FaucetEnabled = enabled
	}
	for flag, dst := range map[string]*math.Int{flagFaucetMaxDrip: &p.FaucetMaxDrip, flagFaucetEpochCap: &p.FaucetEpochCap} {
		if !cmd.Flags().Changed(flag) {
			continue
		}
		raw, err := cmd.Flags().GetString(flag)
		if err != nil {
			return fmt.Errorf("failed to read --%s: %w", flag, err)
		}
		amount, ok := math.NewIntFromString(raw)
		if !ok {
			return fmt.Errorf("invalid --%s %q: want an integer amount of norama", flag, raw)
		}
		*dst = amount
	}
	if cmd.Flags().Changed(flagFaucetCooldown) {
		cooldown, err := cmd.Flags().GetUint64(flagFaucetCooldown)
		if err != nil {
			return fmt.Errorf("failed to read --%s: %w", flagFaucetCooldown, err)
		}
		p.FaucetRecipientCooldownSeconds = cooldown
	}
	return nil
}
