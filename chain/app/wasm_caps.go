package app

// WasmCapabilities are the CosmWasm capabilities this chain advertises.
// stargate and ibc2 are omitted: contracts must not query arbitrary modules or speak IBC (D25).
func WasmCapabilities() []string {
	return []string{
		"iterator",
		"staking",
		"cosmwasm_1_1",
		"cosmwasm_1_2",
		"cosmwasm_1_3",
		"cosmwasm_1_4",
		"cosmwasm_2_0",
		"cosmwasm_2_1",
		"cosmwasm_2_2",
	}
}
