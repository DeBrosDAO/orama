package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	wasmtypes "github.com/CosmWasm/wasmd/x/wasm/types"
	abci "github.com/cometbft/cometbft/abci/types"

	"github.com/cosmos/cosmos-sdk/types/query"

	"github.com/DeBrosOfficial/network/chain/client/node"
	nodestypes "github.com/DeBrosOfficial/network/chain/x/nodes/types"
)

const (
	// standardCodeIDs are the genesis standard contracts, codes 1 to 5 (docs/CHAIN.md, "Genesis
	// standard contracts"): CW20, CW721, escrow, CW3 multisig, vesting.
	cw20CodeID     = 1
	codesPageLimit = 100
	cw20Supply     = "1000"
	cw20Transfer   = "10"
)

var standardCodeIDs = []uint64{1, 2, 3, 4, 5}

// missingCodes are the wanted code ids not in have.
func missingCodes(have []uint64, want []uint64) []uint64 {
	set := map[uint64]bool{}
	for _, id := range have {
		set[id] = true
	}
	var out []uint64
	for _, id := range want {
		if !set[id] {
			out = append(out, id)
		}
	}
	return out
}

// contractAddressFromEvents finds the address wasmd reports in its instantiate event.
func contractAddressFromEvents(events []abci.Event) (string, bool) {
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

// parseCW20Balance reads a cw20 `balance` query answer, {"balance":"<n>"}.
func parseCW20Balance(data []byte) (string, error) {
	var ans struct {
		Balance string `json:"balance"`
	}
	if err := json.Unmarshal(data, &ans); err != nil || ans.Balance == "" {
		return "", fmt.Errorf("not a cw20 balance answer: %q", data)
	}
	return ans.Balance, nil
}

func cw20InstantiateMsg(holder string) ([]byte, error) {
	type coin struct {
		Address string `json:"address"`
		Amount  string `json:"amount"`
	}
	return json.Marshal(map[string]any{
		"name": "Stagenet Smoke", "symbol": "SMK", "decimals": 6,
		"initial_balances": []coin{{Address: holder, Amount: cw20Supply}},
	})
}

// checkCodes: the five standard contracts are stored.
func checkCodes(ctx context.Context, c *node.Client) Result {
	const name = "wasm-codes"
	var resp wasmtypes.QueryCodesResponse
	err := c.Query(ctx, "/cosmwasm.wasm.v1.Query/Codes", &wasmtypes.QueryCodesRequest{Pagination: &query.PageRequest{Limit: codesPageLimit}}, &resp)
	if err != nil {
		return fail(name, "query the stored codes: %v", err)
	}
	have := make([]uint64, len(resp.CodeInfos))
	for i, ci := range resp.CodeInfos {
		have[i] = ci.CodeID
	}
	if missing := missingCodes(have, standardCodeIDs); len(missing) > 0 {
		return fail(name, "codes %v are not stored (have %v)", missing, have)
	}
	return pass(name, "the 5 standard contracts are stored as codes %v", standardCodeIDs)
}

// checkCW20 instantiates a CW20 from code 1 with the operator holding the supply, transfers a few
// tokens to another operator and reads the balance back.
func checkCW20(ctx context.Context, e *env, c *node.Client) Result {
	const name = "wasm-cw20"
	holder := e.signer.AccountAddress()
	recipient, err := e.otherOperator(ctx, c, holder)
	if err != nil {
		return fail(name, "%v", err)
	}
	initMsg, err := cw20InstantiateMsg(holder)
	if err != nil {
		return fail(name, "%v", err)
	}
	_, events, err := c.SubmitWithEvents(ctx, e.signer, &wasmtypes.MsgInstantiateContract{
		Sender: holder, CodeID: cw20CodeID, Label: "stagenet-smoke-cw20", Msg: initMsg,
	})
	if err != nil {
		return fail(name, "instantiate code %d: %v", cw20CodeID, err)
	}
	contract, ok := contractAddressFromEvents(events)
	if !ok {
		return fail(name, "the instantiate transaction reported no contract address")
	}
	transfer, err := json.Marshal(map[string]any{"transfer": map[string]string{"recipient": recipient, "amount": cw20Transfer}})
	if err != nil {
		return fail(name, "%v", err)
	}
	if _, _, err := c.SubmitWithEvents(ctx, e.signer, &wasmtypes.MsgExecuteContract{Sender: holder, Contract: contract, Msg: transfer}); err != nil {
		return fail(name, "transfer on %s: %v", contract, err)
	}
	q, err := json.Marshal(map[string]any{"balance": map[string]string{"address": recipient}})
	if err != nil {
		return fail(name, "%v", err)
	}
	var resp wasmtypes.QuerySmartContractStateResponse
	if err := c.Query(ctx, "/cosmwasm.wasm.v1.Query/SmartContractState", &wasmtypes.QuerySmartContractStateRequest{Address: contract, QueryData: q}, &resp); err != nil {
		return fail(name, "query the balance on %s: %v", contract, err)
	}
	got, err := parseCW20Balance(resp.Data)
	if err != nil {
		return fail(name, "%v", err)
	}
	if got != cw20Transfer {
		return fail(name, "recipient %s holds %s on %s, want %s", recipient, got, contract, cw20Transfer)
	}
	return pass(name, "instantiated %s, transferred %s to %s", contract, cw20Transfer, recipient)
}

// otherOperator is the operator of another registered node than the signer's, so the transfer
// moves tokens between two accounts.
func (e *env) otherOperator(ctx context.Context, c *node.Client, self string) (string, error) {
	for _, n := range e.nodes {
		var resp nodestypes.QueryNodeResponse
		err := c.Query(ctx, "/orama.nodes.v1.Query/Node", &nodestypes.QueryNodeRequest{NodeId: n.Name}, &resp)
		if err != nil {
			var qerr *node.QueryError
			if errors.As(err, &qerr) && qerr.NotFound() {
				continue
			}
			return "", fmt.Errorf("look up node %s: %w", n.Name, err)
		}
		if resp.Node.Operator != self {
			return resp.Node.Operator, nil
		}
	}
	return "", errors.New("no other registered operator to receive the transfer (`orama setup` registers the nodes; run it first)")
}
