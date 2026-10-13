// Package cli holds wasmpolicy's genesis command.
package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/server"
	genutiltypes "github.com/cosmos/cosmos-sdk/x/genutil/types"

	"github.com/DeBrosOfficial/network/chain/contracts/standard"
)

// AddStandardContractsCmd returns the `genesis add-standard-contracts` command. It stores the
// standard contracts (chain/contracts/standard) in genesis.json at --home, so they exist from
// height 1 although upload is closed until upload_sunset_height.
//
// It must run before the chain's first `oramad start`, on a binary built with libwasmvm: a
// binary without it has no wasm module in its default genesis and the command says so.
func AddStandardContractsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "add-standard-contracts",
		Short: "Store the standard contracts (CW20, CW721, escrow, CW3 multisig, vesting) in genesis.json",
		Long: `Add the genesis standard contracts to genesis.json as stored wasm codes (1, 2, ... in manifest order) and list
them in wasmpolicy's genesis code set. Nothing is instantiated: anyone can instantiate them
after genesis. The command refuses a genesis that already has wasm codes.

Run it before the chain's first 'oramad start', with an oramad built with libwasmvm. The
contracts, their source commits and their sha256 are pinned in chain/contracts/standard/manifest.json.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			clientCtx := client.GetClientContextFromCmd(cmd)
			cometConfig := server.GetServerContextFromCmd(cmd).Config
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
			if err := standard.Apply(appState); err != nil {
				return fmt.Errorf("failed to add the standard contracts to %s: %w", genFile, err)
			}
			if appGenesis.AppState, err = json.Marshal(appState); err != nil {
				return fmt.Errorf("failed to marshal app state: %w", err)
			}
			if err := appGenesis.SaveAs(genFile); err != nil {
				return fmt.Errorf("failed to write genesis file %s: %w", genFile, err)
			}
			cmd.Println("standard contracts added to genesis")
			return nil
		},
	}
}
