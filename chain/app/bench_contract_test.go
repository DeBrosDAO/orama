//go:build cgo && !nowasm

package app_test

import (
	"encoding/json"
	"testing"

	abci "github.com/cometbft/cometbft/abci/types"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

const (
	// benchStandardCW20 is the standard contract the benchmark runs, instantiated from its genesis code.
	benchStandardCW20 = "cw20-base"
	// benchCW20Supply is the token balance the benchmark holder starts with.
	benchCW20Supply = "1000000000000"
)

// cw20Bench is an instantiated cw20-base contract and the holder who pays for the calls.
type cw20Bench struct {
	chain    *wasmChain
	contract sdk.AccAddress
	holder   wasmUser
	other    wasmUser
}

// newCW20Bench starts a chain with the standard contracts and instantiates cw20-base with the
// holder owning the whole supply.
func newCW20Bench(b *testing.B) *cw20Bench {
	b.Helper()
	c := newWasmChain(b, wasmChainOptions{users: 2})
	var codeID uint64
	for _, contract := range c.standard.Contracts {
		if contract.Name == benchStandardCW20 {
			codeID = contract.CodeID
		}
	}
	if codeID == 0 {
		b.Fatalf("the standard contracts have no %s", benchStandardCW20)
	}
	holder, other := c.users[0], c.users[1]
	contract := c.instantiate(holder, codeID, map[string]any{
		"name": "Bench", "symbol": "BNCH", "decimals": 6,
		"initial_balances": []map[string]string{{"address": holder.addr.String(), "amount": benchCW20Supply}},
	}, nil)
	return &cw20Bench{chain: c, contract: contract, holder: holder, other: other}
}

// BenchmarkWASMExecute is one cw20-base transfer, a standard contract, run as a transaction in its
// own block: the ante chain, the CosmWasm VM call and the commit.
func BenchmarkWASMExecute(b *testing.B) {
	cw := newCW20Bench(b)
	msg := map[string]any{"transfer": map[string]string{"recipient": cw.other.addr.String(), "amount": "1"}}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		cw.chain.height++
		tx := signedTx(b, cw.chain.app, cw.holder.key, int64(cw.chain.height), wasmGas, cw.chain.execMsg(cw.holder, cw.contract, msg, nil))
		b.StartTimer()
		resp := finalize(b, cw.chain.app, cw.chain.height, cw.chain.at(cw.chain.height), tx)
		b.StopTimer()
		if res := resp.TxResults[0]; res.Code != abci.CodeTypeOK {
			b.Fatalf("the contract call failed: %s", res.Log)
		}
		b.StartTimer()
	}
}

// BenchmarkWASMQuery is one cw20-base balance query through the keeper: the VM call without a block.
func BenchmarkWASMQuery(b *testing.B) {
	cw := newCW20Bench(b)
	query, err := json.Marshal(map[string]any{"balance": map[string]string{"address": cw.holder.addr.String()}})
	if err != nil {
		b.Fatal(err)
	}
	ctx := cw.chain.ctx()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := cw.chain.app.WasmKeeper().QuerySmart(ctx, cw.contract, query); err != nil {
			b.Fatal(err)
		}
	}
}
