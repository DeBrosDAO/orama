package app

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"cosmossdk.io/math"
)

// G1 locks every genesis parameter at the value the plan signs off
// (plans/open-network.md P1-P10 and D12-D17, track-c-chain.md C2-C8,
// track-g-token-legal-security.md G1). The plan values are the module default
// genesis, so the check compares a genesis's "params" object against the app's
// own default genesis, key by key. locked_genesis_test.go pins each default to
// its plan literal and citation, and fails when a module gains a parameter that
// has no row there.
//
// Chain-id classes:
//   - "-localnet-": nothing is locked. Local tests and scripts/localnet need short
//     epochs and one-seat committees.
//   - "-devnet-" and "-stagenet-": everything is locked except the keys in
//     testnetRelaxedParams. Those are the shortened epoch clock and the small
//     committee that chain/scripts/stagenet/deploy.sh sets on purpose.
//   - anything else (a production chain-id): every locked parameter must equal its
//     plan value.
const (
	localnetMarker = "-localnet-"
)

var testnetChainIDMarkers = []string{"-devnet-", "-stagenet-"}

// lockedModules are the genesis modules whose "params" are locked. Each name is
// the module's genesis key.
var lockedModules = []string{
	"emission", "fees", "power", "nodes", "storage", "relay", "token", "archive", "houses",
	"staking", "slashing", "distribution", "shielded",
}

// wasmPolicyModule is locked on its sunset height (P6) and its state-deposit parameters (P3 and the
// C9 bounds). The genesis code set and the deposit ledger are contents of the genesis, not parameters:
// `genesis add-standard-contracts` writes the first and an export carries the second.
const wasmPolicyModule = "wasmpolicy"

var wasmPolicyLocked = map[string]bool{
	"upload_sunset_height": true,
	"deposit_per_byte":     true,
	"max_deposit_per_tx":   true,
	"max_deposit_chunks":   true,
	"chunk_overhead_bytes": true,
}

// testnetRelaxedParams are the "module.key" parameters a devnet or stagenet
// chain-id may change. x/emission and x/power enforce their own production
// floors on the same chain-id split (their InitGenesis chain-id gates).
var testnetRelaxedParams = map[string]bool{
	"emission.epoch_duration_seconds": true,
	"emission.min_blocks_per_epoch":   true,
	"emission.allow_bootstrap_stake":  true,
	"power.min_committee_size":        true,
}

// ValidateLockedGenesis returns an error naming every locked parameter in gs
// that differs from its plan value in defaults, for chainID's class. defaults
// is the app's DefaultGenesis. It never changes gs.
func ValidateLockedGenesis(chainID string, defaults, gs GenesisState) error {
	if strings.Contains(chainID, localnetMarker) {
		return nil
	}
	relaxed := false
	for _, marker := range testnetChainIDMarkers {
		if strings.Contains(chainID, marker) {
			relaxed = true
		}
	}

	var diffs []string
	for _, module := range lockedModules {
		d, err := lockedModuleDiffs(module, defaults, gs, relaxed)
		if err != nil {
			return err
		}
		diffs = append(diffs, d...)
	}
	d, err := wasmPolicyDiffs(defaults, gs)
	if err != nil {
		return err
	}
	diffs = append(diffs, d...)

	if len(diffs) == 0 {
		return nil
	}
	sort.Strings(diffs)
	return fmt.Errorf("genesis parameters differ from the locked G1 values on chain-id %q:\n  %s", chainID, strings.Join(diffs, "\n  "))
}

func lockedModuleDiffs(module string, defaults, gs GenesisState, relaxed bool) ([]string, error) {
	want, err := paramsObject(module, defaults)
	if err != nil {
		return nil, fmt.Errorf("default genesis: %w", err)
	}
	got, err := paramsObject(module, gs)
	if err != nil {
		return nil, fmt.Errorf("genesis: %w", err)
	}
	return objectDiffs(module, want, got, func(key string) bool {
		return relaxed && testnetRelaxedParams[module+"."+key]
	}), nil
}

func wasmPolicyDiffs(defaults, gs GenesisState) ([]string, error) {
	want, err := rawObject(wasmPolicyModule, defaults)
	if err != nil {
		return nil, fmt.Errorf("default genesis: %w", err)
	}
	got, err := rawObject(wasmPolicyModule, gs)
	if err != nil {
		return nil, fmt.Errorf("genesis: %w", err)
	}
	only := func(key string) bool { return !wasmPolicyLocked[key] }
	return objectDiffs(wasmPolicyModule, want, got, only), nil
}

func paramsObject(module string, gs GenesisState) (map[string]json.RawMessage, error) {
	obj, err := rawObject(module, gs)
	if err != nil {
		return nil, err
	}
	raw, ok := obj["params"]
	if !ok {
		return nil, fmt.Errorf("module %q has no params", module)
	}
	var params map[string]json.RawMessage
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, fmt.Errorf("module %q params: %w", module, err)
	}
	return params, nil
}

func rawObject(module string, gs GenesisState) (map[string]json.RawMessage, error) {
	raw, ok := gs[module]
	if !ok {
		return nil, fmt.Errorf("module %q is missing from the genesis state", module)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, fmt.Errorf("module %q genesis: %w", module, err)
	}
	return obj, nil
}

// objectDiffs lists every key, in the union of want and got, whose value
// differs. skip reports keys that are not locked. A key absent from one side
// is a difference unless the other side is also absent (proto JSON omits zero
// values).
func objectDiffs(module string, want, got map[string]json.RawMessage, skip func(string) bool) []string {
	keys := map[string]bool{}
	for k := range want {
		keys[k] = true
	}
	for k := range got {
		keys[k] = true
	}
	var diffs []string
	for k := range keys {
		if skip(k) {
			continue
		}
		w, g := want[k], got[k]
		if !sameJSONValue(w, g) {
			diffs = append(diffs, fmt.Sprintf("%s.%s = %s, plan value %s", module, k, describeJSON(g), describeJSON(w)))
		}
	}
	return diffs
}

func describeJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "(unset)"
	}
	return string(raw)
}

// sameJSONValue compares two JSON values. Numbers and numeric strings compare
// as decimals, so "1", 1 and "1.000000000000000000" are equal; everything else
// compares as compact JSON.
func sameJSONValue(a, b json.RawMessage) bool {
	if len(a) == 0 || len(b) == 0 {
		return len(a) == 0 && len(b) == 0
	}
	if da, ok := asDec(a); ok {
		if db, ok := asDec(b); ok {
			return da.Equal(db)
		}
		return false
	}
	return compactJSON(a) == compactJSON(b)
}

func asDec(raw json.RawMessage) (math.LegacyDec, bool) {
	s := strings.Trim(string(raw), `"`)
	if s == "" || strings.HasPrefix(s, "{") || strings.HasPrefix(s, "[") {
		return math.LegacyDec{}, false
	}
	d, err := math.LegacyNewDecFromStr(s)
	if err != nil {
		return math.LegacyDec{}, false
	}
	return d, true
}

func compactJSON(raw json.RawMessage) string {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	out, err := json.Marshal(v)
	if err != nil {
		return string(raw)
	}
	return string(out)
}
