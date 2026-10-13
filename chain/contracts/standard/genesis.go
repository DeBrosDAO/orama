package standard

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strconv"

	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
)

const (
	wasmModule   = "wasm"
	policyModule = "wasmpolicy"

	// codeCreator names the account recorded as the uploader of every genesis code. It is the
	// wasmpolicy module account: no key controls it, so no one owns the standard contracts.
	codeCreator = policyModule

	// lastCodeIDKey and lastContractIDKey are wasmd's sequence keys (x/wasm/types/keys.go): the
	// 0x04 sequence prefix followed by the sequence name.
	sequencePrefix     = 0x04
	lastCodeIDName     = "lastCodeId"
	lastContractIDName = "lastContractId"
)

type wasmCode struct {
	CodeID    string   `json:"code_id"`
	CodeInfo  codeInfo `json:"code_info"`
	CodeBytes []byte   `json:"code_bytes"`
	Pinned    bool     `json:"pinned"`
}

type codeInfo struct {
	CodeHash          []byte            `json:"code_hash"`
	Creator           string            `json:"creator"`
	InstantiateConfig instantiateConfig `json:"instantiate_config"`
}

type instantiateConfig struct {
	Permission string   `json:"permission"`
	Addresses  []string `json:"addresses"`
}

type wasmSequence struct {
	IDKey []byte `json:"id_key"`
	Value string `json:"value"`
}

// Apply stores the standard contracts in appState, the map of module name to genesis JSON that
// genesis.json carries as app_state. It adds one wasm code per contract (anyone may instantiate it,
// nothing is instantiated), advances wasmd's code sequence past them, and lists their code ids in
// wasmpolicy's genesis code set so a store of those ids stays allowed before the upload sunset.
//
// It refuses to run against a genesis that already has wasm codes or a genesis code set, or that
// has no wasm module (a binary built without libwasmvm has none).
func Apply(appState map[string]json.RawMessage) error {
	m, err := Load()
	if err != nil {
		return err
	}
	wasmGen, err := m.wasmGenesis(appState)
	if err != nil {
		return err
	}
	policyGen, err := policyGenesis(appState, len(m.Contracts))
	if err != nil {
		return err
	}
	appState[wasmModule] = wasmGen
	appState[policyModule] = policyGen
	return nil
}

// wasmGenesis returns the wasm genesis with the standard codes and sequences added.
func (m Manifest) wasmGenesis(appState map[string]json.RawMessage) (json.RawMessage, error) {
	rawWasm, ok := appState[wasmModule]
	if !ok {
		return nil, fmt.Errorf("genesis has no %q module: this oramad is built without libwasmvm and cannot ship contracts", wasmModule)
	}
	var gen map[string]json.RawMessage
	if err := json.Unmarshal(rawWasm, &gen); err != nil {
		return nil, fmt.Errorf("failed to parse the %s genesis: %w", wasmModule, err)
	}
	if err := requireEmpty(gen, "codes", "contracts"); err != nil {
		return nil, err
	}
	var err error
	if gen["codes"], err = json.Marshal(m.wasmCodes()); err != nil {
		return nil, fmt.Errorf("failed to encode the standard codes: %w", err)
	}
	if gen["sequences"], err = sequences(uint64(len(m.Contracts)) + 1); err != nil {
		return nil, err
	}
	out, err := json.Marshal(gen)
	if err != nil {
		return nil, fmt.Errorf("failed to encode the %s genesis: %w", wasmModule, err)
	}
	return out, nil
}

func requireEmpty(gen map[string]json.RawMessage, keys ...string) error {
	for _, key := range keys {
		raw, ok := gen[key]
		if !ok {
			continue
		}
		var list []json.RawMessage
		if err := json.Unmarshal(raw, &list); err != nil {
			return fmt.Errorf("failed to parse wasm genesis %s: %w", key, err)
		}
		if len(list) > 0 {
			return fmt.Errorf("wasm genesis already has %d %s: refusing to add the standard contracts", len(list), key)
		}
	}
	return nil
}

func (m Manifest) wasmCodes() []wasmCode {
	creator := sdk.AccAddress(authtypes.NewModuleAddress(codeCreator)).String()
	out := make([]wasmCode, len(m.Contracts))
	for i, c := range m.Contracts {
		sum := sha256.Sum256(c.Wasm)
		out[i] = wasmCode{
			CodeID: strconv.FormatUint(c.CodeID, 10),
			CodeInfo: codeInfo{
				CodeHash:          sum[:],
				Creator:           creator,
				InstantiateConfig: instantiateConfig{Permission: "Everybody", Addresses: []string{}},
			},
			CodeBytes: c.Wasm,
		}
	}
	return out
}

func sequences(nextCodeID uint64) (json.RawMessage, error) {
	seqs := []wasmSequence{
		{IDKey: append([]byte{sequencePrefix}, lastCodeIDName...), Value: strconv.FormatUint(nextCodeID, 10)},
		{IDKey: append([]byte{sequencePrefix}, lastContractIDName...), Value: "1"},
	}
	raw, err := json.Marshal(seqs)
	if err != nil {
		return nil, fmt.Errorf("failed to encode the wasm sequences: %w", err)
	}
	return raw, nil
}

// policyGenesis returns the wasmpolicy genesis with codes 1..n as its genesis code set.
func policyGenesis(appState map[string]json.RawMessage, n int) (json.RawMessage, error) {
	raw, ok := appState[policyModule]
	if !ok {
		return nil, fmt.Errorf("genesis has no %q module", policyModule)
	}
	var gen map[string]json.RawMessage
	if err := json.Unmarshal(raw, &gen); err != nil {
		return nil, fmt.Errorf("failed to parse the %s genesis: %w", policyModule, err)
	}
	if err := requireEmpty(gen, "genesis_code_ids"); err != nil {
		return nil, err
	}
	ids := make([]uint64, n)
	for i := range ids {
		ids[i] = uint64(i + 1)
	}
	var err error
	if gen["genesis_code_ids"], err = json.Marshal(ids); err != nil {
		return nil, fmt.Errorf("failed to encode the genesis code ids: %w", err)
	}
	out, err := json.Marshal(gen)
	if err != nil {
		return nil, fmt.Errorf("failed to encode the %s genesis: %w", policyModule, err)
	}
	return out, nil
}
