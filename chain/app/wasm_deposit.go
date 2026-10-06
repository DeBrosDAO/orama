//go:build cgo && !nowasm

package app

import (
	"context"
	"fmt"

	wasmvm "github.com/CosmWasm/wasmvm/v3"
	wasmvmtypes "github.com/CosmWasm/wasmvm/v3/types"

	sdk "github.com/cosmos/cosmos-sdk/types"

	wasmkeeper "github.com/CosmWasm/wasmd/x/wasm/keeper"
	wasmtypes "github.com/CosmWasm/wasmd/x/wasm/types"

	"github.com/DeBrosOfficial/network/chain/x/wasmpolicy"
	policykeeper "github.com/DeBrosOfficial/network/chain/x/wasmpolicy/keeper"
)

// depositEngine wraps wasmd's VM engine so every contract call that changes stored bytes settles a
// state deposit (plans/open-network/track-c-chain.md C9: deposits are metered in Orama's wasm
// keeper wrapper, since wasmd has no per-byte deposit). It hands the contract a metered store,
// runs the call, and on success charges growth or releases shrinkage through the persisted ledger
// in x/wasmpolicy. A call that fails is reverted by wasmd and charged nothing.
//
// Queries and code storage do not write contract state and are not wrapped. IBC entry points are
// not wrapped either: this chain has no IBC (D25), so none can run.
type depositEngine struct {
	wasmtypes.WasmEngine
	ledger     policykeeper.Keeper
	isContract func(ctx context.Context, addr sdk.AccAddress) bool
}

func newDepositEngine(old wasmtypes.WasmEngine, ledger policykeeper.Keeper, isContract func(ctx context.Context, addr sdk.AccAddress) bool) depositEngine {
	return depositEngine{WasmEngine: old, ledger: ledger, isContract: isContract}
}

func (e depositEngine) Instantiate(checksum wasmvm.Checksum, env wasmvmtypes.Env, info wasmvmtypes.MessageInfo, initMsg []byte, store wasmvm.KVStore, goapi wasmvm.GoAPI, querier wasmvm.Querier, gasMeter wasmvm.GasMeter, gasLimit uint64, deserCost wasmvmtypes.UFraction) (*wasmvmtypes.ContractResult, uint64, error) {
	ms := wasmpolicy.NewMeteredStore(store)
	res, gas, err := e.WasmEngine.Instantiate(checksum, env, info, initMsg, ms, goapi, querier, gasMeter, gasLimit, deserCost)
	return e.settle(querier, env.Contract.Address, info.Sender, ms, res, gas, err)
}

func (e depositEngine) Execute(checksum wasmvm.Checksum, env wasmvmtypes.Env, info wasmvmtypes.MessageInfo, executeMsg []byte, store wasmvm.KVStore, goapi wasmvm.GoAPI, querier wasmvm.Querier, gasMeter wasmvm.GasMeter, gasLimit uint64, deserCost wasmvmtypes.UFraction) (*wasmvmtypes.ContractResult, uint64, error) {
	ms := wasmpolicy.NewMeteredStore(store)
	res, gas, err := e.WasmEngine.Execute(checksum, env, info, executeMsg, ms, goapi, querier, gasMeter, gasLimit, deserCost)
	return e.settle(querier, env.Contract.Address, info.Sender, ms, res, gas, err)
}

func (e depositEngine) Migrate(checksum wasmvm.Checksum, env wasmvmtypes.Env, migrateMsg []byte, store wasmvm.KVStore, goapi wasmvm.GoAPI, querier wasmvm.Querier, gasMeter wasmvm.GasMeter, gasLimit uint64, deserCost wasmvmtypes.UFraction) (*wasmvmtypes.ContractResult, uint64, error) {
	ms := wasmpolicy.NewMeteredStore(store)
	res, gas, err := e.WasmEngine.Migrate(checksum, env, migrateMsg, ms, goapi, querier, gasMeter, gasLimit, deserCost)
	return e.settle(querier, env.Contract.Address, "", ms, res, gas, err)
}

func (e depositEngine) MigrateWithInfo(checksum wasmvm.Checksum, env wasmvmtypes.Env, migrateMsg []byte, migrateInfo wasmvmtypes.MigrateInfo, store wasmvm.KVStore, goapi wasmvm.GoAPI, querier wasmvm.Querier, gasMeter wasmvm.GasMeter, gasLimit uint64, deserCost wasmvmtypes.UFraction) (*wasmvmtypes.ContractResult, uint64, error) {
	ms := wasmpolicy.NewMeteredStore(store)
	res, gas, err := e.WasmEngine.MigrateWithInfo(checksum, env, migrateMsg, migrateInfo, ms, goapi, querier, gasMeter, gasLimit, deserCost)
	return e.settle(querier, env.Contract.Address, "", ms, res, gas, err)
}

func (e depositEngine) Sudo(checksum wasmvm.Checksum, env wasmvmtypes.Env, sudoMsg []byte, store wasmvm.KVStore, goapi wasmvm.GoAPI, querier wasmvm.Querier, gasMeter wasmvm.GasMeter, gasLimit uint64, deserCost wasmvmtypes.UFraction) (*wasmvmtypes.ContractResult, uint64, error) {
	ms := wasmpolicy.NewMeteredStore(store)
	res, gas, err := e.WasmEngine.Sudo(checksum, env, sudoMsg, ms, goapi, querier, gasMeter, gasLimit, deserCost)
	return e.settle(querier, env.Contract.Address, "", ms, res, gas, err)
}

func (e depositEngine) Reply(checksum wasmvm.Checksum, env wasmvmtypes.Env, reply wasmvmtypes.Reply, store wasmvm.KVStore, goapi wasmvm.GoAPI, querier wasmvm.Querier, gasMeter wasmvm.GasMeter, gasLimit uint64, deserCost wasmvmtypes.UFraction) (*wasmvmtypes.ContractResult, uint64, error) {
	ms := wasmpolicy.NewMeteredStore(store)
	res, gas, err := e.WasmEngine.Reply(checksum, env, reply, ms, goapi, querier, gasMeter, gasLimit, deserCost)
	return e.settle(querier, env.Contract.Address, "", ms, res, gas, err)
}

// settle charges or refunds the call's net storage change. A deposit that cannot be paid turns
// the call into a contract error, which wasmd treats like any other failed execution.
func (e depositEngine) settle(querier wasmvm.Querier, contract, sender string, ms *wasmpolicy.MeteredStore, res *wasmvmtypes.ContractResult, gasUsed uint64, callErr error) (*wasmvmtypes.ContractResult, uint64, error) {
	if callErr != nil || res == nil || res.Err != "" {
		return res, gasUsed, callErr
	}
	grew, shrank := ms.Delta()
	if grew == shrank {
		return res, gasUsed, nil
	}
	handler, ok := querier.(wasmkeeper.QueryHandler)
	if !ok {
		return nil, gasUsed, fmt.Errorf("state deposit metering needs wasmd's query handler, got %T", querier)
	}
	addr, err := sdk.AccAddressFromBech32(contract)
	if err != nil {
		return nil, gasUsed, fmt.Errorf("state deposit: contract address %q: %w", contract, err)
	}
	if err := e.ledger.ApplyStateDelta(handler.Ctx, addr, e.payer(handler.Ctx, sender), grew, shrank); err != nil {
		return &wasmvmtypes.ContractResult{Err: "state deposit: " + err.Error()}, gasUsed, nil
	}
	return res, gasUsed, nil
}

// payer is the message sender when that is a plain account, and otherwise the transaction signer
// the ante chain recorded. A contract never pays: its calls are rooted in a signer.
func (e depositEngine) payer(ctx sdk.Context, sender string) sdk.AccAddress {
	if addr, err := sdk.AccAddressFromBech32(sender); err == nil && !e.isContract(ctx, addr) {
		return addr
	}
	return wasmpolicy.DepositPayer(ctx)
}
