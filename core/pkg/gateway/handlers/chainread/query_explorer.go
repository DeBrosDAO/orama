package chainread

// explorerQuery lists the cosmos-sdk and wasmd Query methods the public route serves beyond the
// wallet's (query_wallet.go), for the explorer's token and contract pages: the denoms the chain
// has issued and who holds one, and the contracts of the chain with their code, history and raw
// state. Each is a point lookup or a walk the caller pages with pagination.limit, which
// queryUpstream holds to queryMaxPageLimit. wasmd's SmartContractState is not here: it runs the
// contract, so its cost is the contract's, and wasmd does not mark it as safe for a public query.
// RawContractState and Code are not here either: the first needs a key the explorer cannot
// guess, and the second answers the whole wasm binary.
var explorerQuery = names(
	"cosmos.bank.v1beta1.Query/TotalSupply", // paginated
	"cosmos.bank.v1beta1.Query/DenomOwners", // paginated
	"cosmwasm.wasm.v1.Query/Codes",          // paginated
	"cosmwasm.wasm.v1.Query/CodeInfo",
	"cosmwasm.wasm.v1.Query/ContractsByCode",    // paginated
	"cosmwasm.wasm.v1.Query/ContractsByCreator", // paginated
	"cosmwasm.wasm.v1.Query/ContractHistory",    // paginated
	"cosmwasm.wasm.v1.Query/AllContractState",   // paginated
)
