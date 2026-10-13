//go:build e2e_fleet

package chainshielded

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
	"github.com/DeBrosOfficial/network/e2e/features/internal/cliconf"
	"github.com/DeBrosOfficial/network/e2e/harness"
)

// These tests put real Halo 2 proofs on the chain. Everything else in this feature
// feeds the chain bundles whose proof is zeros, so no verifier is ever reached.
//
// The bundles come from the wallet crate (chain/x/shielded/wallet), the only builder of valid
// proofs: the committed vectors, which are signed for one fixed chain id and so are real bundles
// for the wrong chain, and a scenario the crate builds for the run's own chain id. A run chain
// built by chain-deploy.sh has neither the Orchard library nor the verifier binary, so on it both
// tests stop at the first refusal, say so, and report not applicable; on a chain whose nodes run
// both verifiers (an installed node, or a chain-deploy.sh build with the library) they reach the
// verifiers.

const (
	// vectorDir holds the committed Ironwood vectors, relative to the checkout.
	vectorDir = "chain/x/shielded/orchardffi/testdata"
	// walletDir is the wallet crate, relative to the checkout.
	walletDir = "chain/x/shielded/wallet"
	// vectorShield is the committed one-action shield: a real proof signed for chain-id in the
	// same directory.
	vectorShield = "ironwood-1-action.bundle"
	// proofFlipOffset is a byte inside the proof of the one-action vector: 1 action byte, one
	// action, the 41 byte header, then the proof.
	proofFlipOffset = 1 + actionBytes + 41 + 600

	wantSignatureRejected = "signature rejected"
	wantProofRejected     = "proof rejected"

	// scenarioBudget bounds the wallet crate's proving of the scenario ("about a minute").
	scenarioBudget = 10 * time.Minute

	// The wallet scenario's amounts, in units its scale multiplies (x/shielded/wallet scenario.rs
	// and chain/scripts/stagenet/smoke/check_shielded.go).
	scenarioShieldUnits     = 10_000 + 7_000
	scenarioUnshieldUnits   = 5_000
	scenarioChangeUnits     = 4_000
	scenarioTransferActions = 2
	scenarioUnshieldActions = 2
	// feeMargin doubles the base fee a transfer's fee is sized for: the base fee moves by up to
	// 12.5% a block.
	feeMargin = 2
)

func readVector(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(cliconf.RepoRoot(t), vectorDir, name))
	if err != nil {
		t.Fatalf("failed to read the committed vector %s: %v", name, err)
	}
	return raw
}

// vectorNullifier is the nullifier of the first action of a bundle.
func vectorNullifier(raw []byte) []byte { return raw[1+nullifierAt : 1+nullifierAt+fieldBytes] }

// requireNullifierUnspent fails when the chain reports nullifier spent.
func requireNullifierUnspent(t *testing.T, c *chain.Chain, nullifier []byte, what string) {
	t.Helper()
	a := c.ABCIQuery(t, c.Node(t, 0), shieldedQuery+"NullifierSpent", chain.PB{}.Bytes(1, nullifier))
	if f, err := chain.DecodePB(a.Value); a.Code != 0 || err != nil || f.Varint(1) != 0 {
		t.Fatalf("%s: the nullifier is spent: code %d %x (%v)", what, a.Code, a.Value, err)
	}
}

// requireVerifierRefusal checks a real-proof bundle was refused, and returns whether a verifier
// was the one that refused it. A chain without the verifiers refuses at the anchor gate (its tree
// stub is not linked), which proves the node fails closed and nothing about the proof.
func requireVerifierRefusal(t *testing.T, what string, r chain.Result, wants ...string) (reachedVerifier bool) {
	t.Helper()
	chain.RequireRefused(t, what, r)
	switch {
	case anyOf(r.Log, wants...):
		return true
	case anyOf(r.Log, wantNotLinked, wantAnchor):
		return false
	}
	t.Fatalf("%s: refused for a reason that is neither a verifier's (%v) nor the missing verifiers': %s", what, wants, r)
	return false
}

// TestShield_realProofBoundToAnotherChainIsRefused: a bundle with a valid Halo 2 proof built for
// another chain id is refused here, because the signatures commit to the chain id (the sighash);
// the same bundle with one bit of its proof flipped is refused too. Neither spends the nullifier
// or moves the pool. The pool was empty, so the bundle's anchor (the empty tree's root) passes
// the anchor gate and the verifiers are what refuse it.
func TestShield_realProofBoundToAnotherChainIsRefused(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := c.FundedValidator(t, 1, chain.Orama(1))
	before := queryPool(t, c)
	vector := readVector(t, vectorShield)

	foreign := c.Submit(t, k, signedOpts, shieldEarningsMsg(k.Address, vector))
	reached := requireVerifierRefusal(t, "a real proof signed for another chain", foreign, wantSignatureRejected)

	tampered := bytes.Clone(vector)
	tampered[proofFlipOffset] ^= 1
	flipped := c.Submit(t, k, signedOpts, shieldEarningsMsg(k.Address, tampered))
	reachedFlipped := requireVerifierRefusal(t, "a real proof with a flipped bit", flipped, wantProofRejected, wantSignatureRejected)

	requireNullifierUnspent(t, c, vectorNullifier(vector), "after the refused real proofs")
	requirePoolUntouched(t, c, before, "real proofs that were refused")
	if !reached || !reachedFlipped {
		harness.SkipNotApplicable(t, "the run chain's nodes have neither the Orchard library nor the verifier binary, so the refusals above came from the anchor gate and no verifier saw the proof; run on a chain whose validators run both (an installed node, or a chain-deploy.sh build with the library) to reach them")
	}
}

// scenarioStep is one step of the wallet builder's output (wallet/src/scenario.rs).
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

func bigOf(t *testing.T, s string) *big.Int {
	t.Helper()
	v, ok := new(big.Int).SetString(s, 10)
	if !ok {
		t.Fatalf("%q is not an integer amount", s)
	}
	return v
}

// scenarioScale is the largest multiplier of the scenario's amounts the chain accepts: the
// unshield goes to the signer's fee-only balance, which x/shielded caps at max_fee_topup per
// unshield (net of its nullifier fees), and the transfer's fee must stay below the change it
// leaves (the stagenet smoke's rule, chain/scripts/stagenet/smoke/check_shielded.go).
func scenarioScale(t *testing.T, p shieldedParams, fee *big.Int) uint64 {
	t.Helper()
	room := new(big.Int).Add(bigOf(t, p.MaxFeeTopup), new(big.Int).Mul(bigOf(t, p.NullifierFee), big.NewInt(scenarioUnshieldActions)))
	scale := new(big.Int).Quo(room, big.NewInt(scenarioUnshieldUnits))
	if scale.Sign() <= 0 || !scale.IsUint64() {
		harness.SkipNotApplicable(t, fmt.Sprintf("max_fee_topup %s leaves no room for the scenario's unshield of %d units", p.MaxFeeTopup, scenarioUnshieldUnits))
	}
	if fee.Cmp(new(big.Int).Mul(scale, big.NewInt(scenarioChangeUnits))) >= 0 {
		harness.SkipNotApplicable(t, fmt.Sprintf("the transfer fee %s is not below the scenario's change at scale %s; max_fee_topup %s is too small", fee, scale, p.MaxFeeTopup))
	}
	return scale.Uint64()
}

// buildScenario runs the wallet crate's gen_scenario for the run's chain id, the signer of the
// unshield and the scale and fee the chain accepts, and returns its steps.
func buildScenario(t *testing.T, chainID, signerHex string, scale uint64, fee *big.Int) scenario {
	t.Helper()
	cargo, err := exec.LookPath("cargo")
	if err != nil {
		harness.SkipNotApplicable(t, "the runner has no Rust toolchain (cargo) to build the wallet scenario with; install it to run the shielded success path")
	}
	ctx, cancel := context.WithTimeout(context.Background(), scenarioBudget)
	defer cancel()
	cmd := exec.CommandContext(ctx, cargo, "run", "--release", "--locked", "--example", "gen_scenario")
	cmd.Dir = filepath.Join(cliconf.RepoRoot(t), walletDir)
	cmd.Env = append(os.Environ(),
		"ORAMA_SCENARIO_CHAIN_ID="+chainID,
		"ORAMA_SCENARIO_UNSHIELD_SIGNER="+signerHex,
		fmt.Sprintf("ORAMA_SCENARIO_SCALE=%d", scale),
		"ORAMA_SCENARIO_FEE="+fee.String(),
	)
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to build the wallet scenario: %v\n%s", err, stderr.String())
	}
	var sc scenario
	if err := json.Unmarshal(out.Bytes(), &sc); err != nil {
		t.Fatalf("the wallet scenario is not JSON: %v", err)
	}
	want := []string{"shield", "shield", "transfer", "unshield"}
	if sc.ChainID != chainID || len(sc.Steps) != len(want) {
		t.Fatalf("the wallet scenario is for %q with %d steps, want %q with %d", sc.ChainID, len(sc.Steps), chainID, len(want))
	}
	for i, kind := range want {
		if sc.Steps[i].Kind != kind {
			t.Fatalf("scenario step %d is %q, want %q", i, sc.Steps[i].Kind, kind)
		}
	}
	return sc
}

// TestShieldedPool_walletScenarioShieldTransferUnshield: the success path with real proofs. The
// wallet crate builds, for this chain's id and against the note tree as each step leaves it, two
// shields from the validator's earnings, a signer-less transfer and an unshield to its fee-only
// balance; every step is accepted by both verifiers on every validator, the note tree grows by
// each bundle's commitments, the nullifier set by each bundle's nullifiers, and the module's
// invariants hold on every validator afterwards. It needs a chain whose validators run both
// verifiers, a Rust toolchain on the runner and an empty note tree (the scenario's first anchor
// is the empty tree's), and reports not applicable otherwise.
func TestShieldedPool_walletScenarioShieldTransferUnshield(t *testing.T) {
	c := chain.New(t)
	k := c.Validator(t, c.Node(t, 1))
	if before := queryPool(t, c); before.treeSize != 0 {
		harness.SkipNotApplicable(t, fmt.Sprintf("the note tree already holds %d notes and the scenario starts from an empty one; run it on a chain that has seen no shielded bundle", before.treeSize))
	}
	probe := c.Submit(t, k, signedOpts, shieldEarningsMsg(k.Address, readVector(t, vectorShield)))
	if !requireVerifierRefusal(t, "the probe, a real proof for another chain", probe, wantSignatureRejected) {
		harness.SkipNotApplicable(t, "the run chain's nodes have neither the Orchard library nor the verifier binary, so no bundle can be accepted; run on a chain whose validators run both")
	}

	p := queryParams(t, c)
	node := c.Node(t, 0)
	bf := c.BaseFee(t, node)
	baseFee := new(big.Int).Set(&bf.Int)
	gas := new(big.Int).Mul(new(big.Int).SetUint64(p.ActionGas), big.NewInt(scenarioTransferActions))
	fee := new(big.Int).Mul(gas, baseFee)
	fee.Mul(fee, big.NewInt(feeMargin))
	fee.Add(fee, new(big.Int).Mul(bigOf(t, p.NullifierFee), big.NewInt(scenarioTransferActions)))
	fee.Add(fee, big.NewInt(1))
	scale := scenarioScale(t, p, fee)

	need := new(big.Int).Mul(new(big.Int).SetUint64(scale), big.NewInt(scenarioShieldUnits))
	need.Add(need, new(big.Int).Mul(bigOf(t, p.NullifierFee), big.NewInt(2*scenarioTransferActions)))
	need.Add(need, big.NewInt(chain.NoramaPerOrama))
	if have := c.Earnings(t, k.Node, k.Address); have.Int.Cmp(need) < 0 {
		harness.SkipNotApplicable(t, fmt.Sprintf("%s holds %s norama of earnings and the scenario shields %s at scale %d; wait for more epochs", k.Node.Name, have, need, scale))
	}
	signer, err := chain.Bech32Decode("orama", k.Address)
	if err != nil {
		t.Fatalf("the validator address %q is not bech32: %v", k.Address, err)
	}
	sc := buildScenario(t, c.ID, hex.EncodeToString(signer), scale, fee)

	notes, nullifiers := uint64(0), uint64(0)
	for _, step := range sc.Steps {
		bundle, err := hex.DecodeString(step.Bundle)
		if err != nil || len(bundle) == 0 {
			t.Fatalf("step %s: the bundle is not hex", step.Name)
		}
		var r chain.Result
		switch step.Kind {
		case "shield":
			r = c.Submit(t, k, signedOpts, shieldEarningsMsg(k.Address, bundle))
		case "transfer":
			actions := len(step.Cmxs)
			r = c.BroadcastUnsigned(t, c.Node(t, 2), chain.TxOptions{Gas: transferGas(p, actions)}, transferMsg(signerless(), bundle))
		case "unshield":
			r = c.Submit(t, k, signedOpts, unshieldMsg(k.Address, bundle, nil))
		}
		chain.RequireOK(t, "scenario step "+step.Name+" ("+step.Kind+")", r)
		notes += uint64(len(step.Cmxs))
		nullifiers += uint64(len(step.Cmxs))
	}

	after := queryPool(t, c)
	if after.treeSize != notes {
		t.Errorf("the note tree holds %d notes after the scenario, want one per action: %d", after.treeSize, notes)
	}
	if after.nullifiers != nullifiers {
		t.Errorf("the nullifier set holds %d after the scenario, want one per action: %d", after.nullifiers, nullifiers)
	}
	for _, n := range c.Nodes() {
		if got := queryFieldsOn(t, c, n, "TreeState", chain.PB{}).Varint(1); got != notes {
			t.Errorf("%s reports %d notes, want %d: the validators disagree", n.Name, got, notes)
		}
	}
	c.RequireInvariants(t, "the wallet scenario with real proofs")
}
