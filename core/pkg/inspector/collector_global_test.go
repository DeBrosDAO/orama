package inspector

import (
	"testing"
	"time"
)

func TestParseGlobalCollect_peersAndHotKey(t *testing.T) {
	const stdout = `
===ORAMA_GLOBAL chain_load===
loaded
===ORAMA_GLOBAL chain_state===
active
===ORAMA_GLOBAL status===
{"jsonrpc":"2.0","id":-1,"result":{"node_info":{"id":"aa","network":"orama-stagenet-1","version":"0.39.4"},"sync_info":{"latest_block_height":"100","latest_block_time":"2026-09-28T00:00:00Z","catching_up":false},"validator_info":{"address":"0000000000000000000000000000000000000000","voting_power":"10"}}}
===ORAMA_GLOBAL net===
{"jsonrpc":"2.0","id":-1,"result":{"n_peers":"0"}}
===ORAMA_GLOBAL validators===
{"jsonrpc":"2.0","id":-1,"result":{"validators":[],"total":"3"}}
===ORAMA_GLOBAL params===
{"params":{"signed_blocks_window":"100","min_signed_per_window":"0.900000000000000000"}}
===ORAMA_GLOBAL signing===
{"info":[]}
===ORAMA_GLOBAL staking===
{"validators":[]}
===ORAMA_GLOBAL ipfs_load===
not-found
===ORAMA_GLOBAL ipfs_state===
inactive
===ORAMA_GLOBAL repo===
===ORAMA_GLOBAL provider_load===
loaded
===ORAMA_GLOBAL provider_state===
active
===ORAMA_GLOBAL provider_monitor===
{"hot_key_balance_norama":0}
===ORAMA_GLOBAL relay_load===
not-found
===ORAMA_GLOBAL relay_state===
inactive
===ORAMA_GLOBAL relay_monitor===
`
	chain, global := parseGlobalCollect(stdout, time.Date(2026, 9, 28, 0, 0, 30, 0, time.UTC))
	if chain == nil || !chain.Responsive || chain.Peers != 0 || chain.ValidatorCount != 3 {
		t.Fatalf("chain %+v", chain)
	}
	if chain.BlockAgeSec < 29 || chain.BlockAgeSec > 31 {
		t.Fatalf("block age %v", chain.BlockAgeSec)
	}
	if chain.SigningError == "" {
		t.Fatal("a validator missing from staking was treated as found")
	}
	if global == nil || global.Provider == nil || global.Provider.HotKeyBalanceNorama == nil || *global.Provider.HotKeyBalanceNorama != 0 {
		t.Fatalf("global %+v", global)
	}
}
