package app_test

import (
	"encoding/json"
	"fmt"
	"testing"

	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/log/v2"
	dbm "github.com/cosmos/cosmos-db"

	"github.com/cosmos/cosmos-sdk/baseapp"
	servertypes "github.com/cosmos/cosmos-sdk/server/types"

	"github.com/DeBrosOfficial/network/chain/app"
	policytypes "github.com/DeBrosOfficial/network/chain/x/wasmpolicy/types"
)

func TestWasmPolicyInDefaultGenesis(t *testing.T) {
	oramaApp := buildTestApp(t)
	gen := app.NewDefaultGenesisState(oramaApp)
	raw := gen[policytypes.ModuleName]
	require.NotEmpty(t, raw)
	var gs policytypes.GenesisState
	require.NoError(t, json.Unmarshal(raw, &gs))
	require.Equal(t, policytypes.DefaultUploadSunsetHeight, gs.UploadSunsetHeight)
	require.Empty(t, gs.GenesisCodeIDs)
	require.NoError(t, gs.Validate())
}

func TestIBCIsNotAWiredModule(t *testing.T) {
	oramaApp := buildTestApp(t)
	for _, name := range []string{"ibc", "transfer", "interchainaccounts"} {
		require.NotContains(t, oramaApp.ModuleManager.Modules, name)
	}
	require.Contains(t, oramaApp.ModuleManager.Modules, policytypes.ModuleName)
	_, hasWasm := oramaApp.ModuleManager.Modules["wasm"]
	require.Equal(t, app.WasmVMLinked(), hasWasm)
}

func TestCapabilitiesOmitIBCAndStargate(t *testing.T) {
	for _, cap := range app.WasmCapabilities() {
		require.NotEqual(t, "stargate", cap)
		require.NotEqual(t, "ibc2", cap)
		require.NotContains(t, cap, "ibc")
	}
}

type wasmClaimOptions struct{}

func (wasmClaimOptions) Get(key string) interface{} {
	if key == "wasm.memory_cache_size" {
		return uint32(1)
	}
	return nil
}

func TestNowasmRefusesANodeThatClaimsWasm(t *testing.T) {
	if app.WasmVMLinked() {
		t.Skip("cgo build links libwasmvm")
	}
	app.SetAddressPrefixes()
	var opts servertypes.AppOptions = wasmClaimOptions{}
	defer func() {
		recovered := recover()
		require.NotNil(t, recovered)
		require.Contains(t, fmt.Sprint(recovered), "nowasm")
	}()
	_ = app.NewOramaApp(log.NewNopLogger(), dbm.NewMemDB(), false, opts, baseapp.SetChainID(testChainID))
	t.Fatal("NewOramaApp returned")
}

func TestNowasmInitChainRejectsWasmGenesis(t *testing.T) {
	if app.WasmVMLinked() {
		t.Skip("cgo build links libwasmvm")
	}
	oramaApp := buildTestApp(t)
	gen, _ := buildGenesisState(t, oramaApp)
	gen["wasm"] = []byte(`{}`)
	bz, err := json.Marshal(gen)
	require.NoError(t, err)
	_, err = oramaApp.InitChain(&abci.RequestInitChain{
		ChainId:       testChainID,
		InitialHeight: 1,
		AppStateBytes: bz,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "nowasm")
}
