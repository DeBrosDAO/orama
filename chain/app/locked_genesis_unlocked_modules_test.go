package app_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DeBrosOfficial/network/chain/app"
)

// Every module that has genesis parameters and is not on the locked list can be changed by a
// production genesis without ValidateLockedGenesis noticing: auth prices signatures and tx bytes,
// bank can switch sending off, wasm sets who may upload and instantiate.
func TestLockedGenesis_authBankAndWasmParametersAreLockedOnProduction(t *testing.T) {
	defaults := buildTestApp(t).DefaultGenesis()
	mutations := map[string]map[string]any{
		"bank": {"default_send_enabled": false},
		"auth": {"sig_verify_cost_ed25519": "0", "tx_size_cost_per_byte": "0", "tx_sig_limit": "1000"},
		"wasm": {"instantiate_default_permission": "Nobody"},
	}
	for module, keys := range mutations {
		if module == "wasm" && !app.WasmVMLinked() {
			continue
		}
		for key, value := range keys {
			t.Run(module+"."+key, func(t *testing.T) {
				gs := cloneGenesis(t, defaults)
				var obj map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(gs[module], &obj))
				var params map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(obj["params"], &params))
				raw, err := json.Marshal(value)
				require.NoError(t, err)
				params[key] = raw
				obj["params"], err = json.Marshal(params)
				require.NoError(t, err)
				gs[module], err = json.Marshal(obj)
				require.NoError(t, err)
				require.Error(t, app.ValidateLockedGenesis("orama-1", defaults, gs),
					"a production genesis changed %s.%s and ValidateLockedGenesis accepted it", module, key)
			})
		}
	}
}
