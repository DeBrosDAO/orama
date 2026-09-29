package standard_test

import (
	"crypto/sha256"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/app"
	"github.com/DeBrosOfficial/network/chain/contracts/standard"
)

func TestLoad_everyArtifactMatchesItsPinnedHash(t *testing.T) {
	m, err := standard.Load()
	require.NoError(t, err)
	require.Equal(t, []string{"cw20-base", "cw721-base", "cw20-escrow", "cw3-fixed-multisig", "cw-vesting"}, names(m))
	for i, c := range m.Contracts {
		require.Equal(t, uint64(i+1), c.CodeID, c.Name)
		require.NotEmpty(t, c.Wasm, c.Name)
		require.Len(t, c.Source.Commit, 40, "%s pins a full git commit", c.Name)
		require.NotEmpty(t, c.Source.Tag, c.Name)
		sum := sha256.Sum256(c.Wasm)
		require.Equal(t, c.SHA256, hexOf(sum[:]), c.Name)
	}
	require.Equal(t, "1.81.0", m.Toolchain.Rust)
}

func names(m standard.Manifest) []string {
	out := make([]string, len(m.Contracts))
	for i, c := range m.Contracts {
		out[i] = c.Name
	}
	return out
}

func hexOf(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, len(b)*2)
	for _, x := range b {
		out = append(out, digits[x>>4], digits[x&0xf])
	}
	return string(out)
}

func genesisWith(wasm, policy string) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	if wasm != "" {
		out["wasm"] = json.RawMessage(wasm)
	}
	if policy != "" {
		out["wasmpolicy"] = json.RawMessage(policy)
	}
	return out
}

const (
	emptyWasm   = `{"params":{"code_upload_access":{"permission":"Everybody","addresses":[]},"instantiate_default_permission":"Everybody"},"codes":[],"contracts":[],"sequences":[]}`
	emptyPolicy = `{"upload_sunset_height":100,"genesis_code_ids":[],"deposit_per_byte":"1","deposit_chunks":[]}`
)

func TestApply_storesEveryContractAndListsItsIDs(t *testing.T) {
	app.SetAddressPrefixes()
	state := genesisWith(emptyWasm, emptyPolicy)
	require.NoError(t, standard.Apply(state))

	var wasmGen struct {
		Codes []struct {
			CodeID   string `json:"code_id"`
			CodeInfo struct {
				CodeHash []byte `json:"code_hash"`
				Creator  string `json:"creator"`
			} `json:"code_info"`
			CodeBytes []byte `json:"code_bytes"`
		} `json:"codes"`
		Sequences []struct {
			IDKey []byte `json:"id_key"`
			Value string `json:"value"`
		} `json:"sequences"`
		Params json.RawMessage `json:"params"`
	}
	require.NoError(t, json.Unmarshal(state["wasm"], &wasmGen))
	m, err := standard.Load()
	require.NoError(t, err)
	require.Len(t, wasmGen.Codes, len(m.Contracts))
	for i, c := range wasmGen.Codes {
		sum := sha256.Sum256(m.Contracts[i].Wasm)
		require.Equal(t, sum[:], c.CodeInfo.CodeHash)
		require.Equal(t, m.Contracts[i].Wasm, c.CodeBytes)
		_, err := sdk.AccAddressFromBech32(c.CodeInfo.Creator)
		require.NoError(t, err)
	}
	require.Contains(t, string(wasmGen.Params), "Everybody", "the wasm params are left alone")
	require.Len(t, wasmGen.Sequences, 2)
	require.Equal(t, "\x04lastCodeId", string(wasmGen.Sequences[0].IDKey))
	require.Equal(t, "6", wasmGen.Sequences[0].Value, "the next code id follows the standard ones")

	var policy struct {
		Sunset uint64   `json:"upload_sunset_height"`
		IDs    []uint64 `json:"genesis_code_ids"`
		PerB   string   `json:"deposit_per_byte"`
	}
	require.NoError(t, json.Unmarshal(state["wasmpolicy"], &policy))
	require.Equal(t, []uint64{1, 2, 3, 4, 5}, policy.IDs)
	require.Equal(t, uint64(100), policy.Sunset, "the rest of the policy genesis is kept")
	require.Equal(t, "1", policy.PerB)
}

func TestApply_refusesWhatItCannotAddTo(t *testing.T) {
	app.SetAddressPrefixes()

	t.Run("no wasm module: a binary built without libwasmvm", func(t *testing.T) {
		err := standard.Apply(genesisWith("", emptyPolicy))
		require.ErrorContains(t, err, "without libwasmvm")
	})
	t.Run("no policy module", func(t *testing.T) {
		require.ErrorContains(t, standard.Apply(genesisWith(emptyWasm, "")), "wasmpolicy")
	})
	t.Run("codes already present", func(t *testing.T) {
		state := genesisWith(emptyWasm, emptyPolicy)
		require.NoError(t, standard.Apply(state))
		require.ErrorContains(t, standard.Apply(state), "already has")
	})
	t.Run("a genesis code set already present", func(t *testing.T) {
		policy := `{"upload_sunset_height":100,"genesis_code_ids":[7],"deposit_per_byte":"1","deposit_chunks":[]}`
		require.ErrorContains(t, standard.Apply(genesisWith(emptyWasm, policy)), "already has")
	})
	t.Run("malformed genesis", func(t *testing.T) {
		require.Error(t, standard.Apply(genesisWith(`[]`, emptyPolicy)))
		require.Error(t, standard.Apply(genesisWith(`{"codes":{}}`, emptyPolicy)))
	})
}

func TestApply_leavesTheStateUntouchedWhenItRefuses(t *testing.T) {
	app.SetAddressPrefixes()
	state := genesisWith(emptyWasm, `{"upload_sunset_height":1,"genesis_code_ids":[7],"deposit_per_byte":"1","deposit_chunks":[]}`)
	require.Error(t, standard.Apply(state))
	require.JSONEq(t, emptyWasm, string(state["wasm"]), "a refused apply must not leave codes behind")
}
