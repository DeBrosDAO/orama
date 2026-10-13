package gen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	wasmtypes "github.com/CosmWasm/wasmd/x/wasm/types"
	abci "github.com/cometbft/cometbft/abci/types"

	"github.com/cosmos/cosmos-sdk/types/query"

	"github.com/DeBrosOfficial/network/chain/client/node"
)

// WasmCall is the type name of a call to a standard contract.
const WasmCall = "wasm-call"

const (
	// cw20CodeID is the genesis standard CW20 (contracts/standard: codes 1 to 5 are CW20, CW721,
	// escrow, CW3 multisig and vesting).
	cw20CodeID = 1
	// cw20Holding is what every account starts with, so transfers of one unit never run dry.
	cw20Holding   = "1000000000"
	cw20Transfer  = "1"
	codesPageSize = 10
)

func init() { register(WasmCall, func() Generator { return &wasmCall{} }) }

// wasmCall executes a CW20 `transfer` of one unit on a contract instantiated for the run.
type wasmCall struct {
	contract string
	accounts []string
}

func (w *wasmCall) Name() string { return WasmCall }

func (w *wasmCall) Describe() string {
	return "wasm MsgExecuteContract: a CW20 transfer of 1 unit on a genesis standard contract (code 1) instantiated for the run"
}

func (w *wasmCall) Prepare(ctx context.Context, env *Env, _ int) error {
	var codes wasmtypes.QueryCodesResponse
	err := env.Chain.Client.Query(ctx, "/cosmwasm.wasm.v1.Query/Codes", &wasmtypes.QueryCodesRequest{Pagination: &query.PageRequest{Limit: codesPageSize}}, &codes)
	var refusal *node.QueryError
	if errors.As(err, &refusal) {
		return Skipf("the node does not serve CosmWasm (build it with WITH_WASM=1): %v", err)
	}
	if err != nil {
		return fmt.Errorf("list the stored wasm codes: %w", err)
	}
	if !hasCode(codes.CodeInfos, cw20CodeID) {
		return Skipf("the genesis has no standard CW20 (code %d); start the localnet with WITH_WASM=1", cw20CodeID)
	}
	creator := env.Accounts[0]
	holders := make([]map[string]string, len(env.Accounts))
	w.accounts = make([]string, len(env.Accounts))
	for i, a := range env.Accounts {
		w.accounts[i] = a.Address
		holders[i] = map[string]string{"address": a.Address, "amount": cw20Holding}
	}
	initMsg, err := json.Marshal(map[string]any{"name": "Bench CW20", "symbol": "BNCH", "decimals": 6, "initial_balances": holders})
	if err != nil {
		return fmt.Errorf("encode the CW20 instantiate message: %w", err)
	}
	_, events, err := env.Chain.Client.SubmitWithEvents(ctx, creator, &wasmtypes.MsgInstantiateContract{
		Sender: creator.Address, CodeID: cw20CodeID, Label: "oramad-bench-cw20", Msg: initMsg,
	})
	if err != nil {
		return Skipf("a CW20 cannot be instantiated from code %d: %v", cw20CodeID, err)
	}
	contract, ok := contractAddress(events)
	if !ok {
		return fmt.Errorf("the CW20 instantiate transaction reported no contract address")
	}
	w.contract = contract
	env.Log("instantiated CW20 %s for %d holders", contract, len(w.accounts))
	return nil
}

func hasCode(infos []wasmtypes.CodeInfoResponse, id uint64) bool {
	for _, ci := range infos {
		if ci.CodeID == id {
			return true
		}
	}
	return false
}

// contractAddress finds the address wasmd reports in its instantiate event.
func contractAddress(events []abci.Event) (string, bool) {
	for _, ev := range events {
		if ev.Type != wasmtypes.EventTypeInstantiate {
			continue
		}
		for _, a := range ev.Attributes {
			if a.Key == wasmtypes.AttributeKeyContractAddr && a.Value != "" {
				return a.Value, true
			}
		}
	}
	return "", false
}

func (w *wasmCall) Available() int { return Unlimited }

func (w *wasmCall) Plan(k int) (Planned, error) {
	from, to := pair(k, len(w.accounts))
	body, err := json.Marshal(map[string]any{"transfer": map[string]string{"recipient": w.accounts[to], "amount": cw20Transfer}})
	if err != nil {
		return Planned{}, fmt.Errorf("encode the CW20 transfer: %w", err)
	}
	return Planned{Account: from, Msg: &wasmtypes.MsgExecuteContract{Sender: w.accounts[from], Contract: w.contract, Msg: body}}, nil
}
