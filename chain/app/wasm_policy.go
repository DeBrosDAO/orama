package app

import (
	"github.com/cosmos/cosmos-sdk/runtime"
	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	"github.com/cosmos/cosmos-sdk/types/module"

	wasmpolicy "github.com/DeBrosOfficial/network/chain/x/wasmpolicy"
	"github.com/DeBrosOfficial/network/chain/x/wasmpolicy/ante"
	"github.com/DeBrosOfficial/network/chain/x/wasmpolicy/keeper"
	"github.com/DeBrosOfficial/network/chain/x/wasmpolicy/types"
)

func (app *OramaApp) mountWasmPolicy(keys map[string]*storetypes.KVStoreKey) {
	if _, ok := keys[types.StoreKey]; !ok {
		keys[types.StoreKey] = storetypes.NewKVStoreKey(types.StoreKey)
	}
	app.WasmPolicyKeeper = keeper.NewKeeper(runtime.NewKVStoreService(keys[types.StoreKey]))
	app.uploadSunset = ante.NewUploadSunsetDecorator(app.WasmPolicyKeeper)
}

func policyModule(k keeper.Keeper) module.AppModule {
	return wasmpolicy.NewAppModule(k)
}

// insertBefore inserts extra immediately before the first occurrence of marker.
func insertBefore(order []string, marker string, extra ...string) []string {
	if len(extra) == 0 {
		return order
	}
	out := make([]string, 0, len(order)+len(extra))
	placed := false
	for _, name := range order {
		if !placed && name == marker {
			out = append(out, extra...)
			placed = true
		}
		out = append(out, name)
	}
	if !placed {
		out = append(out, extra...)
	}
	return out
}
