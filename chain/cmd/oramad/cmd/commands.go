package cmd

import (
	"encoding/json"
	"errors"
	"fmt"

	cmtcfg "github.com/cometbft/cometbft/config"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"cosmossdk.io/log/v2"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/debug"
	"github.com/cosmos/cosmos-sdk/client/keys"
	"github.com/cosmos/cosmos-sdk/client/pruning"
	"github.com/cosmos/cosmos-sdk/client/rpc"
	"github.com/cosmos/cosmos-sdk/client/snapshot"
	"github.com/cosmos/cosmos-sdk/server"
	serverconfig "github.com/cosmos/cosmos-sdk/server/config"
	servertypes "github.com/cosmos/cosmos-sdk/server/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"
	authcmd "github.com/cosmos/cosmos-sdk/x/auth/client/cli"
	genutilcli "github.com/cosmos/cosmos-sdk/x/genutil/client/cli"
	genutiltypes "github.com/cosmos/cosmos-sdk/x/genutil/types"

	"github.com/DeBrosOfficial/network/chain/app"
	"github.com/DeBrosOfficial/network/chain/app/params"
	emissioncli "github.com/DeBrosOfficial/network/chain/x/emission/client/cli"
	powercli "github.com/DeBrosOfficial/network/chain/x/power/client/cli"
	wasmpolicycli "github.com/DeBrosOfficial/network/chain/x/wasmpolicy/client/cli"
)

// initCometBFTConfig returns oramad's default CometBFT config overrides. None are needed today.
func initCometBFTConfig() *cmtcfg.Config {
	return cmtcfg.DefaultConfig()
}

// defaultMinGasPriceNorama is a placeholder floor, not the real fee mechanism:
// plans/open-network/track-c-chain.md C2's EIP-1559-style base fee (with its 100%-burned floor)
// hasn't been built yet, so this just stops a validator from accepting free transactions by
// default in the meantime. It is deliberately nonzero (unlike a bare devnet default of 0) so a
// validator that never touches its own app.toml still charges something.
const defaultMinGasPriceNorama = "0.000001"

// initAppConfig returns oramad's default app.toml template and config.
//
// AppDBBackend defaults to pebbledb, not the SDK's own default (goleveldb): goleveldb, as shipped
// in cosmos-sdk v0.54.4's store/v2 v2.0.0 + cosmos-db v1.1.3, cannot answer a versioned query
// (`CacheMultiStoreWithVersion`) for ANY height, including the just-committed latest one - every
// `oramad query ...` fails with "version does not exist" even though blocks commit and state
// changes correctly (confirmed by an isolated repro against store/v2 directly: identical
// mount/commit/query code succeeds against MemDB and PebbleDB but fails against GoLevelDB alone).
// PebbleDB is pure Go (no cgo/RocksDB dependency) and does not hit this bug, so it is oramad's
// default until upstream fixes goleveldb support.
func initAppConfig() (string, interface{}) {
	srvCfg := serverconfig.DefaultConfig()
	srvCfg.MinGasPrices = defaultMinGasPriceNorama + params.BaseDenom
	srvCfg.AppDBBackend = "pebbledb"
	return serverconfig.DefaultConfigTemplate, srvCfg
}

func initRootCmd(
	rootCmd *cobra.Command,
	txConfig client.TxConfig,
	basicManager module.BasicManager,
) {
	cfg := sdk.GetConfig()
	cfg.Seal()

	rootCmd.AddCommand(
		genutilcli.InitCmd(basicManager, app.DefaultNodeHome),
		debug.Cmd(),
		pruning.Cmd(newApp, app.DefaultNodeHome),
		snapshot.Cmd(newApp),
	)

	server.AddCommandsWithStartCmdOptions(rootCmd, app.DefaultNodeHome, newApp, appExport, server.StartCmdOptions{
		AddFlags: func(startCmd *cobra.Command) {
			startCmd.Flags().String(app.FlagShieldedVerifier, "",
				"path of the out-of-process shielded proof verifier (default <home>/bin/"+app.ShieldedVerifierBinary+")")
		},
	})

	rootCmd.AddCommand(
		server.StatusCommand(),
		genesisCommand(txConfig, basicManager),
		queryCommand(),
		txCommand(),
		keys.Commands(),
	)
}

// genesisCommand builds the `oramad genesis` command, including x/emission's
// set-emission-params, x/power's add-bootstrap-validator and wasmpolicy's add-standard-contracts helpers alongside the standard
// genutil subcommands.
func genesisCommand(txConfig client.TxConfig, basicManager module.BasicManager) *cobra.Command {
	cmd := genutilcli.Commands(txConfig, basicManager, app.DefaultNodeHome)
	cmd.AddCommand(emissioncli.SetEmissionParamsCmd(app.DefaultNodeHome))
	cmd.AddCommand(powercli.AddBootstrapValidatorCmd(app.DefaultNodeHome))
	cmd.AddCommand(wasmpolicycli.AddStandardContractsCmd())
	lockValidateCommand(cmd, basicManager)
	return cmd
}

// lockValidateCommand makes `oramad genesis validate` also run the G1 locked-parameter
// check (app.ValidateLockedGenesis) after genutil's own module validation, on the genesis
// file's chain-id. InitChain runs the same check, so a genesis that passes here starts.
func lockValidateCommand(genesisCmd *cobra.Command, basicManager module.BasicManager) {
	for _, sub := range genesisCmd.Commands() {
		if sub.Name() != "validate" {
			continue
		}
		validate := sub.RunE
		sub.RunE = func(cmd *cobra.Command, args []string) error {
			if err := validate(cmd, args); err != nil {
				return err
			}
			return validateLockedGenesisFile(cmd, args, basicManager)
		}
		return
	}
}

func validateLockedGenesisFile(cmd *cobra.Command, args []string, basicManager module.BasicManager) error {
	path := server.GetServerContextFromCmd(cmd).Config.GenesisFile()
	if len(args) == 1 {
		path = args[0]
	}
	appGenesis, err := genutiltypes.AppGenesisFromFile(path)
	if err != nil {
		return fmt.Errorf("failed to read genesis %s: %w", path, err)
	}
	var genState app.GenesisState
	if err := json.Unmarshal(appGenesis.AppState, &genState); err != nil {
		return fmt.Errorf("failed to parse app_state of %s: %w", path, err)
	}
	defaults := basicManager.DefaultGenesis(client.GetClientContextFromCmd(cmd).Codec)
	if err := app.ValidateLockedGenesis(appGenesis.ChainID, defaults, genState); err != nil {
		return fmt.Errorf("genesis file %s: %w", path, err)
	}
	return nil
}

func queryCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:                        "query",
		Aliases:                    []string{"q"},
		Short:                      "Querying subcommands",
		DisableFlagParsing:         false,
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}

	cmd.AddCommand(
		rpc.WaitTxCmd(),
		server.QueryBlockCmd(),
		authcmd.QueryTxsByEventsCmd(),
		server.QueryBlocksCmd(),
		authcmd.QueryTxCmd(),
		server.QueryBlockResultsCmd(),
	)

	return cmd
}

func txCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:                        "tx",
		Short:                      "Transactions subcommands",
		DisableFlagParsing:         false,
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}

	cmd.AddCommand(
		authcmd.GetSignCommand(),
		authcmd.GetSignBatchCommand(),
		authcmd.GetMultiSignCommand(),
		authcmd.GetMultiSignBatchCmd(),
		authcmd.GetValidateSignaturesCommand(),
		authcmd.GetBroadcastCommand(),
		authcmd.GetEncodeCommand(),
		authcmd.GetDecodeCommand(),
		authcmd.GetSimulateCmd(),
	)

	return cmd
}

// newApp creates the Orama application.
func newApp(logger log.Logger, db dbm.DB, appOpts servertypes.AppOptions) servertypes.Application {
	baseappOptions := server.DefaultBaseappOptions(appOpts)
	return app.NewOramaApp(logger, db, true, appOpts, baseappOptions...)
}

// appExport creates a new OramaApp (optionally at a given height) and exports its state.
func appExport(
	logger log.Logger,
	db dbm.DB,
	height int64,
	forZeroHeight bool,
	jailAllowedAddrs []string,
	appOpts servertypes.AppOptions,
	modulesToExport []string,
) (servertypes.ExportedApp, error) {
	viperAppOpts, ok := appOpts.(*viper.Viper)
	if !ok {
		return servertypes.ExportedApp{}, errors.New("appOpts is not viper.Viper")
	}
	viperAppOpts.Set(server.FlagInvCheckPeriod, 1)
	appOpts = viperAppOpts

	var oramaApp *app.OramaApp
	if height != -1 {
		oramaApp = app.NewOramaApp(logger, db, false, appOpts)
		if err := oramaApp.LoadHeight(height); err != nil {
			return servertypes.ExportedApp{}, err
		}
	} else {
		oramaApp = app.NewOramaApp(logger, db, true, appOpts)
	}

	return oramaApp.ExportAppStateAndValidators(forZeroHeight, jailAllowedAddrs, modulesToExport)
}
