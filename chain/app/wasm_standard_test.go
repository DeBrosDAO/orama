//go:build cgo && !nowasm

package app_test

import (
	"crypto/sha256"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"

	wasmtypes "github.com/CosmWasm/wasmd/x/wasm/types"

	policytypes "github.com/DeBrosOfficial/network/chain/x/wasmpolicy/types"
)

func TestStandardContracts_areStoredAtGenesisAndInThePolicyCodeSet(t *testing.T) {
	c := newWasmChain(t, wasmChainOptions{users: 1})
	require.Len(t, c.standard.Contracts, 5)

	ctx := c.ctx()
	set, err := c.app.WasmPolicyKeeper.GenesisCodeSet(ctx)
	require.NoError(t, err)
	require.Len(t, set, len(c.standard.Contracts))
	for _, contract := range c.standard.Contracts {
		info := c.app.WasmKeeper().GetCodeInfo(ctx, contract.CodeID)
		require.NotNil(t, info, contract.Name)
		sum := sha256.Sum256(contract.Wasm)
		require.Equal(t, sum[:], info.CodeHash, "%s is stored under the checksum of the pinned wasm", contract.Name)
		require.Equal(t, wasmtypes.AccessTypeEverybody, info.InstantiateConfig.Permission, contract.Name)
		_, inSet := set[contract.CodeID]
		require.True(t, inSet, contract.Name)
	}
	require.Equal(t, []string{"cw20-base", "cw721-base", "cw20-escrow", "cw3-fixed-multisig", "cw-vesting"}, standardNames(c))

	// Nothing is instantiated: no contract has an address yet, and the standard code's creator is
	// the wasmpolicy module account, which no key controls.
	info := c.app.WasmKeeper().GetCodeInfo(ctx, 1)
	require.Equal(t, sdk.AccAddress(moduleAddress(policytypes.ModuleName)).String(), info.Creator)
}

func standardNames(c *wasmChain) []string {
	names := make([]string, len(c.standard.Contracts))
	for i, contract := range c.standard.Contracts {
		names[i] = contract.Name
	}
	return names
}

func TestStandardContracts_genesisExportRoundTripsTheCodesAndPolicy(t *testing.T) {
	c := newWasmChain(t, wasmChainOptions{users: 1})
	exported, err := c.app.ExportAppStateAndValidators(false, nil, nil)
	require.NoError(t, err)

	var state map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(exported.AppState, &state))
	var wasmGen wasmtypes.GenesisState
	require.NoError(t, c.app.AppCodec().UnmarshalJSON(state["wasm"], &wasmGen))
	require.Len(t, wasmGen.Codes, 5)
	var policy policytypes.GenesisState
	require.NoError(t, json.Unmarshal(state["wasmpolicy"], &policy))
	require.Equal(t, []uint64{1, 2, 3, 4, 5}, policy.GenesisCodeIDs)
	require.NoError(t, policy.Validate())
}

// TestStandardContracts_passTheChainsCapabilityCheck stores each pinned wasm again through the
// checked path (the one a post-sunset upload takes). A contract that needed a capability this chain
// does not advertise, stargate or ibc for example, would be refused here.
func TestStandardContracts_passTheChainsCapabilityCheck(t *testing.T) {
	c := newWasmChain(t, wasmChainOptions{users: 1})
	c.write(func(ctx sdk.Context) {
		for _, contract := range c.standard.Contracts {
			_, _, err := c.app.WasmContractKeeper().Create(ctx, c.users[0].addr, contract.Wasm, nil)
			require.NoError(t, err, contract.Name)
		}
	})
}
