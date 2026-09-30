package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"

	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkbech32 "github.com/cosmos/cosmos-sdk/types/bech32"

	"cosmossdk.io/math"

	shieldedtypes "github.com/DeBrosOfficial/network/chain/x/shielded/types"
)

const (
	shieldedName = "shielded"
	// scenarioUnshieldUnits and scenarioChangeUnits are the wallet scenario's unshield and the
	// transfer's change before the fee, in units its scale multiplies (x/shielded/wallet scenario.rs).
	scenarioUnshieldUnits = 5_000
	scenarioChangeUnits   = 4_000
	// scenarioUnshieldActions is the unshield bundle's action count: Orchard pads a bundle to two
	// actions, and each spends a nullifier the unshield pays a fee for.
	scenarioUnshieldActions = 2
	// feeMargin doubles the base fee the transfer's fee is sized for, since the base fee moves by up
	// to 12.5% a block.
	feeMargin = 2
)

// scenarioStep is one step of the wallet builder's output (chain/x/shielded/wallet, scenario.rs).
type scenarioStep struct {
	Name         string   `json:"name"`
	Kind         string   `json:"kind"`
	Bundle       string   `json:"bundle"`
	Anchor       string   `json:"anchor"`
	ValueBalance int64    `json:"value_balance"`
	Binding      string   `json:"binding"`
	Cmxs         []string `json:"cmxs"`
}

type scenario struct {
	ChainID string         `json:"chain_id"`
	Steps   []scenarioStep `json:"steps"`
}

// loadScenario reads a scenario and checks it is for chainID with the four steps this runner knows.
func loadScenario(data []byte, chainID string) (scenario, error) {
	var sc scenario
	if err := json.Unmarshal(data, &sc); err != nil {
		return sc, fmt.Errorf("the scenario is not JSON: %w", err)
	}
	if sc.ChainID != chainID {
		return sc, fmt.Errorf("the scenario is for chain %q, not %q (generate it with `deploy.sh gen-shielded`)", sc.ChainID, chainID)
	}
	want := []string{"shield", "shield", "transfer", "unshield"}
	if len(sc.Steps) != len(want) {
		return sc, fmt.Errorf("the scenario has %d steps, want %d", len(sc.Steps), len(want))
	}
	for i, k := range want {
		if sc.Steps[i].Kind != k {
			return sc, fmt.Errorf("step %d is %q, want %q", i, sc.Steps[i].Kind, k)
		}
	}
	return sc, nil
}

// anchorMatches: the wallet built each step against its own note tree, so the chain's tree root
// before a step must equal the step's anchor. The first step's anchor is the empty tree's; the
// chain reports no root for an empty tree.
func anchorMatches(step scenarioStep, root []byte, treeSize uint64, first bool) error {
	if first && treeSize == 0 {
		return nil
	}
	if first {
		return fmt.Errorf("the note tree already holds %d notes; the scenario needs an empty pool", treeSize)
	}
	if got := hex.EncodeToString(root); got != step.Anchor {
		return fmt.Errorf("the chain's tree root is %s, the wallet built %s for its anchor", got, step.Anchor)
	}
	return nil
}

// stepMessage turns a scenario step into the message the chain takes for it, signed by signer. A
// signer-less message is reported so it goes out with no signature and exactly gas.
func stepMessage(step scenarioStep, signer string, actionGas uint64) (msg sdk.Msg, gas uint64, signerless bool, err error) {
	bundle, err := hex.DecodeString(step.Bundle)
	if err != nil || len(bundle) == 0 {
		return nil, 0, false, fmt.Errorf("step %s: the bundle is not hex", step.Name)
	}
	switch step.Kind {
	case "shield":
		// A stagenet account holds earnings, never a bank balance (docs/CHAIN.md: no premine, and
		// users cannot send norama to each other), so a shield comes out of earnings.
		return &shieldedtypes.MsgShieldEarnings{Signer: signer, Bundle: bundle}, 0, false, nil
	case "transfer":
		return &shieldedtypes.MsgShieldedTransfer{Signer: shieldedtypes.SignerlessAddress().String(), Bundle: bundle},
			uint64(len(step.Cmxs)) * actionGas, true, nil
	case "unshield":
		m := &shieldedtypes.MsgUnshield{Signer: signer, Bundle: bundle, Target: shieldedtypes.UnshieldTargetFeeTopup}
		binding, err := m.Binding()
		if err != nil {
			return nil, 0, false, fmt.Errorf("step %s: compute the binding: %w", step.Name, err)
		}
		if got := hex.EncodeToString(binding); got != step.Binding {
			return nil, 0, false, fmt.Errorf("step %s: the wallet signed for binding %s, the chain computes %s for %s (was the scenario built for this operator?)", step.Name, step.Binding, got, signer)
		}
		return m, 0, false, nil
	}
	return nil, 0, false, fmt.Errorf("step %s: unknown kind %q", step.Name, step.Kind)
}

// suggestedTransferFee is the fee a scenario transfer must leave: the base fee of its gas at twice
// the current base fee, plus one nullifier fee per action (a transfer of n actions spends n
// nullifiers), plus one so rounding never leaves it short.
func suggestedTransferFee(actions, actionGas uint64, baseFee, nullifierFee math.Int) math.Int {
	gas := math.NewIntFromUint64(actions).Mul(math.NewIntFromUint64(actionGas))
	base := gas.Mul(baseFee).MulRaw(feeMargin)
	return base.Add(nullifierFee.MulRaw(int64(actions))).AddRaw(1)
}

// scenarioScaleFor is the largest multiplier of the wallet scenario's amounts the chain accepts.
// The scenario unshields to its signer's fee-only balance, which x/shielded caps at max_fee_topup
// per unshield (net of its nullifier fees), and its transfer's fee must stay below the change it
// leaves. A scale fixed without the cap (1,000,000) made the unshield 4998000000 norama over a
// 10000000 cap on stagenet.
func scenarioScaleFor(maxFeeTopup, nullifierFee, fee math.Int) (uint64, error) {
	room := maxFeeTopup.Add(nullifierFee.MulRaw(scenarioUnshieldActions))
	scale := room.QuoRaw(scenarioUnshieldUnits)
	if !scale.IsPositive() || !scale.IsUint64() {
		return 0, fmt.Errorf("max_fee_topup %s leaves no room for the scenario's unshield of %d units", maxFeeTopup, scenarioUnshieldUnits)
	}
	if fee.GTE(scale.MulRaw(scenarioChangeUnits)) {
		return 0, fmt.Errorf("the transfer fee %s is not below the scenario's change at scale %s (%d units); max_fee_topup %s is too small for this fee",
			fee, scale, scenarioChangeUnits, maxFeeTopup)
	}
	return scale.Uint64(), nil
}

// addressBytesHex is the 20 address bytes of an orama bech32 account, in hex, as the wallet
// builder's ORAMA_SCENARIO_UNSHIELD_SIGNER takes them.
func addressBytesHex(addr string) (string, error) {
	prefix, raw, err := sdkbech32.DecodeAndConvert(addr)
	if err != nil {
		return "", fmt.Errorf("%s is not a bech32 address: %w", addr, err)
	}
	if prefix != "orama" || len(raw) != 20 {
		return "", fmt.Errorf("%s is not an orama account address", addr)
	}
	return hex.EncodeToString(raw), nil
}

// checkShielded runs the wallet scenario against the chain, one step at a time, checking the
// chain's tree root against each step's anchor before it goes in.
func checkShielded(ctx context.Context, e *env) Result {
	if e.scenario == "" {
		return skip(shieldedName, "no scenario file: run `deploy.sh gen-shielded` and pass it with SHIELDED_SCENARIO")
	}
	data, err := os.ReadFile(e.scenario)
	if err != nil {
		return fail(shieldedName, "%v", err)
	}
	sc, err := loadScenario(data, e.chainID)
	if err != nil {
		return fail(shieldedName, "%v", err)
	}
	c, err := e.client(ctx, e.nodes[0])
	if err != nil {
		return fail(shieldedName, "%v", err)
	}
	var params shieldedtypes.QueryParamsResponse
	if err := c.Query(ctx, "/orama.shielded.v1.Query/Params", &shieldedtypes.QueryParamsRequest{}, &params); err != nil {
		return fail(shieldedName, "read the shielded params: %v", err)
	}
	total := uint64(0)
	for i, step := range sc.Steps {
		var tree shieldedtypes.QueryTreeStateResponse
		if err := c.Query(ctx, "/orama.shielded.v1.Query/TreeState", &shieldedtypes.QueryTreeStateRequest{}, &tree); err != nil {
			return fail(shieldedName, "step %s: read the note tree: %v", step.Name, err)
		}
		if err := anchorMatches(step, tree.CurrentRoot, tree.TreeSize, i == 0); err != nil {
			return fail(shieldedName, "step %s: %v", step.Name, err)
		}
		msg, gas, signerless, err := stepMessage(step, e.signer.AccountAddress(), params.Params.ActionGas)
		if err != nil {
			return fail(shieldedName, "%v", err)
		}
		if signerless {
			_, _, err = c.SubmitSignerless(ctx, gas, msg)
		} else {
			_, _, err = c.SubmitWithEvents(ctx, e.signer, msg)
		}
		if err != nil {
			return fail(shieldedName, "step %s (%s): %v", step.Name, step.Kind, err)
		}
		total += uint64(len(step.Cmxs))
	}
	var after shieldedtypes.QueryTreeStateResponse
	if err := c.Query(ctx, "/orama.shielded.v1.Query/TreeState", &shieldedtypes.QueryTreeStateRequest{}, &after); err != nil {
		return fail(shieldedName, "read the note tree after the scenario: %v", err)
	}
	if after.TreeSize != total {
		return fail(shieldedName, "the note tree holds %d notes after the scenario, want %d", after.TreeSize, total)
	}
	return pass(shieldedName, "shield, shield, transfer and unshield went through in order, each against the wallet's anchor; the tree holds %d notes (the unshield may be queued by the 24-hour cap)", total)
}
