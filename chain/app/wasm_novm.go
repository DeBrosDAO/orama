//go:build nowasm || !cgo

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cast"

	"github.com/cosmos/cosmos-sdk/client/flags"
	servertypes "github.com/cosmos/cosmos-sdk/server/types"
	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"

	"github.com/DeBrosOfficial/network/chain/x/wasmpolicy/ante"
	"github.com/DeBrosOfficial/network/chain/x/wasmpolicy/types"
)

// errWasmClaim is returned by a binary built without libwasmvm when the node claims wasm.
const errWasmClaim = "oramad built with -tags nowasm refuses to start a node that claims wasm: libwasmvm is not linked"

func wasmModuleAccountPerms() map[string][]string { return nil }

// WasmVMLinked reports whether this binary links libwasmvm.
func WasmVMLinked() bool { return false }

func (app *OramaApp) installWasm(keys map[string]*storetypes.KVStoreKey, appOpts servertypes.AppOptions) {
	if err := guardWasmClaim(appOpts); err != nil {
		panic(err)
	}
	app.mountWasmPolicy(keys)
	app.contractSend = ante.NewFeeEarningsDecorator(func(context.Context, sdk.AccAddress) bool { return false })
	app.wasmModules = []module.AppModule{policyModule(app.WasmPolicyKeeper)}
	app.wasmGenesisOrder = []string{types.ModuleName}
}

func guardWasmClaim(appOpts servertypes.AppOptions) error {
	if appOpts == nil {
		return nil
	}
	for _, key := range []string{
		"wasm.memory_cache_size",
		"wasm.query_gas_limit",
		"wasm.simulation_gas_limit",
		"wasm.skip_wasmvm_version_check",
	} {
		if appOpts.Get(key) != nil {
			return fmt.Errorf("%s (%s)", errWasmClaim, key)
		}
	}
	home := cast.ToString(appOpts.Get(flags.FlagHome))
	if home == "" {
		return nil
	}
	return wasmClaimInGenesisFile(home + "/config/genesis.json")
}

func wasmClaimInGenesisFile(path string) error {
	bz, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read genesis for wasm claim: %w", err)
	}
	var doc struct {
		AppState map[string]json.RawMessage `json:"app_state"`
	}
	if err := json.Unmarshal(bz, &doc); err != nil {
		return fmt.Errorf("parse genesis for wasm claim: %w", err)
	}
	if _, ok := doc.AppState["wasm"]; ok {
		return fmt.Errorf("%s (genesis module wasm)", errWasmClaim)
	}
	return nil
}

func guardWasmGenesis(genesis map[string]json.RawMessage) error {
	if _, ok := genesis["wasm"]; ok {
		return fmt.Errorf("%s (genesis module wasm)", errWasmClaim)
	}
	return nil
}
