package nodenames

// reservedNames mirrors x/nodes' ReservedNames (chain/x/nodes/types/name.go): the labels no
// operator can claim. The chain refuses them in a claim; the sync refuses them again so that a
// bad chain state still cannot publish them. TestReservedNames_matchTheChain fails when the two
// lists differ.
var reservedNames = map[string]bool{
	"www": true, "api": true, "admin": true, "gateway": true, "explorer": true, "status": true, "releases": true,
	"mail": true, "root": true, "dns": true, "mx": true, "smtp": true, "imap": true, "pop": true, "ftp": true, "ssh": true,
	"vpn": true, "cdn": true, "docs": true, "blog": true, "app": true, "dev": true, "test": true, "staging": true,
	"rpc": true, "rest": true, "grpc": true, "node": true, "nodes": true, "validator": true, "validators": true,
	"relay": true, "bootstrap": true, "faucet": true, "wallet": true, "auth": true, "login": true, "git": true,
	"orama": true, "network": true, "support": true, "security": true, "abuse": true, "postmaster": true,
	"hostmaster": true, "webmaster": true, "localhost": true, "monitor": true, "metrics": true, "download": true,
	"downloads": true, "update": true, "updates": true, "install": true, "stagenet": true, "testnet": true,
	"mainnet": true, "devnet": true,
}
