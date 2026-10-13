package app_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	cmtprototypes "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/log/v2"

	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/client/flags"
	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"
	upgradetypes "github.com/cosmos/cosmos-sdk/x/upgrade/types"

	"github.com/DeBrosOfficial/network/chain/app"
	"github.com/DeBrosOfficial/network/chain/app/upgrades"
)

const examplePlanName = "example-plan"

// examplePlan is a test-only governed upgrade: its handler records that it ran and what the
// module manager it was given held.
func examplePlan(ran *int, modules *int) upgrades.Upgrade {
	return upgrades.Upgrade{
		Name: examplePlanName,
		CreateHandler: func(d upgrades.Deps) upgradetypes.UpgradeHandler {
			return func(ctx context.Context, plan upgradetypes.Plan, from module.VersionMap) (module.VersionMap, error) {
				*ran++
				*modules = len(d.ModuleManager.Modules)
				return d.ModuleManager.RunMigrations(ctx, d.Configurator, from)
			}
		},
		StoreUpgrades: &storetypes.StoreUpgrades{Added: []string{"examplestore"}},
	}
}

// runToHeight commits blocks 1..last on an app, with the plan examplePlanName scheduled at
// planHeight (0 schedules none) and list registered from that height (from the first block when
// none is scheduled), and returns the error of the first block that fails.
func runToHeight(t *testing.T, list []upgrades.Upgrade, planHeight, last int64) (*app.OramaApp, error) {
	t.Helper()
	app.SetAddressPrefixes()
	oramaApp := app.NewOramaApp(log.NewNopLogger(), dbm.NewMemDB(), true, simtestutil.AppOptionsMap{flags.FlagHome: t.TempDir()}, baseapp.SetChainID(testChainID))
	t.Cleanup(func() { require.NoError(t, oramaApp.Close()) })
	genState, _ := buildGenesisState(t, oramaApp)
	stateBytes, err := json.Marshal(genState)
	require.NoError(t, err)
	genesisTime := time.Unix(1_700_000_000, 0)
	_, err = oramaApp.InitChain(&abci.RequestInitChain{
		ChainId: testChainID, InitialHeight: 1, Time: genesisTime, AppStateBytes: stateBytes,
		ConsensusParams: &cmtprototypes.ConsensusParams{
			Block:     &cmtprototypes.BlockParams{MaxBytes: 1 << 20, MaxGas: -1},
			Evidence:  &cmtprototypes.EvidenceParams{MaxAgeNumBlocks: 100, MaxAgeDuration: time.Hour, MaxBytes: 1024},
			Validator: &cmtprototypes.ValidatorParams{PubKeyTypes: []string{"ed25519"}},
		},
	})
	require.NoError(t, err)
	for h := int64(1); h <= last; h++ {
		// x/upgrade panics ("BINARY UPDATED BEFORE TRIGGER") when the binary already has the handler
		// of a plan that is scheduled but not yet due. The binary that carries a plan's handler
		// is the one cosmovisor starts at the plan's height, so the handlers are registered
		// there, as the swap to the new binary does.
		if h == planHeight || (planHeight == 0 && h == 1) {
			oramaApp.SetUpgradesForTest(list)
		}
		if h == 2 && planHeight > 0 {
			ctx := sdk.NewContext(oramaApp.CommitMultiStore(), cmtprototypes.Header{Height: 1, Time: genesisTime}, false, oramaApp.Logger())
			require.NoError(t, oramaApp.UpgradeKeeper.ScheduleUpgrade(ctx, upgradetypes.Plan{Name: examplePlanName, Height: planHeight}))
		}
		if _, err := oramaApp.FinalizeBlock(&abci.RequestFinalizeBlock{Height: h, Time: genesisTime.Add(time.Duration(h) * time.Second)}); err != nil {
			return oramaApp, err
		}
		if _, err := oramaApp.Commit(); err != nil {
			return oramaApp, err
		}
	}
	return oramaApp, nil
}

func TestUpgrades_aRegisteredHandlerRunsAtThePlansHeight(t *testing.T) {
	var ran, modules int
	oramaApp, err := runToHeight(t, []upgrades.Upgrade{examplePlan(&ran, &modules)}, 4, 5)
	require.NoError(t, err)
	require.Equal(t, 1, ran, "the handler runs once, at the plan's height")
	require.Positive(t, modules, "the handler was given the app's module manager")

	ctx := sdk.NewContext(oramaApp.CommitMultiStore(), cmtprototypes.Header{Height: 5}, false, oramaApp.Logger())
	done, err := oramaApp.UpgradeKeeper.GetDoneHeight(ctx, examplePlanName)
	require.NoError(t, err)
	require.Equal(t, int64(4), done, "x/upgrade records the plan as done")
	_, err = oramaApp.UpgradeKeeper.GetUpgradePlan(ctx)
	require.Error(t, err, "the plan is cleared once its handler ran")
}

// x/upgrade writes the plan's upgrade-info.json under the app's home at <home>/data. A test app with
// no home wrote it under the working directory, which is the package's source tree, and left a file
// there for git to see.
func TestUpgrades_schedulingAPlanWritesNothingIntoTheSourceTree(t *testing.T) {
	_, err := runToHeight(t, nil, 4, 5)
	require.ErrorContains(t, err, "NEEDED", "a binary with no handler dumps the plan to disk and halts")
	require.NoDirExists(t, "data", "the plan's upgrade-info.json belongs under the test app's temporary home")
}

func TestUpgrades_aPlanWithoutAHandlerHaltsTheChainAtItsHeight(t *testing.T) {
	_, err := runToHeight(t, nil, 4, 5)
	require.Error(t, err, "a binary with no handler for the plan must not go on past its height")
	require.ErrorContains(t, err, "UPGRADE \""+examplePlanName+"\" NEEDED")
}

func TestUpgrades_noPlanMeansNoHandlerRuns(t *testing.T) {
	var ran, modules int
	_, err := runToHeight(t, []upgrades.Upgrade{examplePlan(&ran, &modules)}, 0, 5)
	require.NoError(t, err)
	require.Zero(t, ran)
}

func TestUpgrades_theStoreLoaderIsSetOnlyForARegisteredPlanThatChangesStores(t *testing.T) {
	var ran, modules int
	list := []upgrades.Upgrade{examplePlan(&ran, &modules), {Name: "no-stores", CreateHandler: examplePlan(&ran, &modules).CreateHandler}}
	plan := func(name string) upgradetypes.Plan { return upgradetypes.Plan{Name: name, Height: 10} }

	require.NotNil(t, app.UpgradeStoreLoaderForTest(list, plan(examplePlanName), false))
	require.Nil(t, app.UpgradeStoreLoaderForTest(list, plan(examplePlanName), true), "an operator-skipped height keeps the stores as they are")
	require.Nil(t, app.UpgradeStoreLoaderForTest(list, plan("no-stores"), false))
	require.Nil(t, app.UpgradeStoreLoaderForTest(list, plan("unregistered"), false))
	require.Nil(t, app.UpgradeStoreLoaderForTest(list, upgradetypes.Plan{}, false), "no upgrade info on disk")
	require.Nil(t, app.UpgradeStoreLoaderForTest(nil, plan(examplePlanName), false))
}

func TestUpgrades_theRegistryTheBinaryShipsIsWired(t *testing.T) {
	require.NoError(t, upgrades.CheckRelease(upgrades.Registered(), upgrades.Current()))
	oramaApp := buildTestApp(t)
	for _, u := range upgrades.Registered() {
		require.True(t, oramaApp.UpgradeKeeper.HasHandler(u.Name), "the plan %q has a handler on the app", u.Name)
	}
}
