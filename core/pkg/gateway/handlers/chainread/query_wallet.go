package chainread

// walletQuery lists the cosmos-sdk and wasmd Query methods the public route serves to a wallet
// that has no node of its own: balances, the account, staking positions and rewards of one
// address, the staking pool and parameters, and whether an address is a contract. Each is a point
// lookup, a constant, or a walk of one address's own entries; the ones that list take a
// pagination.limit, which queryUpstream holds to queryMaxPageLimit. The services are embedded
// whole (core/pkg/chainread/gen.sh), so anything not named here, such as bank's whole-chain
// Supply or staking's Validators, is refused as an unknown query.
var walletQuery = names(
	"cosmos.bank.v1beta1.Query/Balance",
	"cosmos.bank.v1beta1.Query/AllBalances",       // paginated
	"cosmos.bank.v1beta1.Query/SpendableBalances", // paginated
	"cosmos.auth.v1beta1.Query/Account", "cosmos.auth.v1beta1.Query/AccountInfo",
	"cosmos.staking.v1beta1.Query/Delegation",
	"cosmos.staking.v1beta1.Query/DelegatorDelegations", // paginated
	"cosmos.staking.v1beta1.Query/UnbondingDelegation",
	"cosmos.staking.v1beta1.Query/DelegatorUnbondingDelegations", // paginated
	"cosmos.staking.v1beta1.Query/Validator", "cosmos.staking.v1beta1.Query/Pool", "cosmos.staking.v1beta1.Query/Params",
	"cosmos.distribution.v1beta1.Query/DelegationRewards", "cosmos.distribution.v1beta1.Query/DelegationTotalRewards",
	// A non-contract address is a 404, so a wallet can refuse a send to a contract before signing.
	"cosmwasm.wasm.v1.Query/ContractInfo",
)
