package removenode

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/production/chainreach"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/production/decommission"
	"github.com/DeBrosOfficial/network/pkg/inspector"
	"github.com/DeBrosOfficial/network/pkg/onchain"
)

const operatorAddr = "orama1operator"

// fakeTx records the retirements a step sends.
type fakeTx struct {
	operator   string
	operatorEr error
	retireErr  error
	retired    []string
}

func (f *fakeTx) Operator(context.Context) (string, error) { return f.operator, f.operatorEr }

func (f *fakeTx) RetireNode(_ context.Context, id string) (*onchain.Receipt, error) {
	if f.retireErr != nil {
		return nil, f.retireErr
	}
	f.retired = append(f.retired, id)
	return &onchain.Receipt{Hash: "ABC", Height: 42}, nil
}

// world is the cluster and the chain a step is run against.
type world struct {
	probes    map[string]string // host -> probe answer
	validator map[string]bool
	unknown   map[string]bool // hosts with no telemetry report
	valErr    error
	asked     []string // hosts whose validator status was read
	chain     *chainreach.ChainNode
	openErr   error
	opened    [][]string
	tx        *fakeTx
	clientErr error
	// chainID is the chain the environment is pinned to; pinErr and checkErr are the refusals.
	chainID  string
	pinErr   error
	checkErr error
	pinned   []string
	checked  []string
}

func newWorld() *world {
	return &world{
		probes:    map[string]string{"10.0.0.1": "yes no\n", "10.0.0.2": "yes no\n", "10.0.0.3": "no no\n"},
		validator: map[string]bool{},
		unknown:   map[string]bool{},
		tx:        &fakeTx{operator: operatorAddr},
		chainID:   "orama-stagenet-1",
	}
}

func (w *world) step(opts Options) *chainStep {
	return &chainStep{
		ctx: context.Background(), env: "stagenet", opts: opts,
		runner: chainreach.Runner{
			Output: func(n inspector.Node, _ string) (string, error) { return w.probes[n.Host], nil },
			Tunnel: func(_ context.Context, n inspector.Node, _ string) (string, func() error, error) {
				if w.openErr != nil {
					return "", nil, w.openErr
				}
				w.opened = append(w.opened, []string{n.Host})
				return "127.0.0.1:1", func() error { return nil }, nil
			},
		},
		validator: func(_ context.Context, _, host string) (bool, bool, error) {
			w.asked = append(w.asked, host)
			return w.validator[host], !w.unknown[host], w.valErr
		},
		readNode: func(context.Context, *chainreach.Reach, string) (*chainreach.ChainNode, error) {
			return w.chain, nil
		},
		newClient: func(context.Context, *chainreach.Reach, string) (txClient, error) { return w.tx, w.clientErr },
		pin: func(env, explicit string) (string, error) {
			w.pinned = append(w.pinned, env+"/"+explicit)
			return w.chainID, w.pinErr
		},
		checkChain: func(_ context.Context, _ *chainreach.Reach, want string) error {
			w.checked = append(w.checked, want)
			return w.checkErr
		},
	}
}

func plan() *decommission.Plan {
	nodes := []inspector.Node{{Host: "10.0.0.1"}, {Host: "10.0.0.2"}, {Host: "10.0.0.3"}}
	return &decommission.Plan{Nodes: nodes, Target: nodes[2]}
}

func activeNode() *chainreach.ChainNode {
	return &chainreach.ChainNode{ID: "node-3", Operator: operatorAddr, Status: "NODE_STATUS_ACTIVE", Endpoints: []string{"10.0.0.3:31000"}}
}

func TestPreflight_aNodeInTheValidatorSetIsNotErasedByAccident(t *testing.T) {
	w := newWorld()
	w.probes["10.0.0.3"] = "yes no\n"
	w.validator["10.0.0.3"] = true

	_, err := w.step(Options{Node: "10.0.0.3"}).Preflight(plan())

	if clierr.CodeOf(err) != clierr.CodeConflict || !strings.Contains(err.Error(), "--drop-validator") {
		t.Fatalf("err = %v, want a conflict naming --drop-validator", err)
	}
}

func TestPreflight_dropValidatorAndOfflineAcceptTheLoss(t *testing.T) {
	for name, opts := range map[string]Options{
		"drop-validator": {Node: "10.0.0.3", DropValidator: true, NoChain: true},
		"offline":        {Node: "10.0.0.3", Offline: true, NoChain: true},
	} {
		w := newWorld()
		w.probes["10.0.0.3"] = "yes no\n"
		w.validator["10.0.0.3"] = true

		if _, err := w.step(opts).Preflight(plan()); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestPreflight_aNodeWithNoTelemetryReportIsNotAssumedToBeNoValidator(t *testing.T) {
	w := newWorld()
	w.probes["10.0.0.3"] = "yes no\n"
	w.unknown["10.0.0.3"] = true

	_, err := w.step(Options{Node: "10.0.0.3", NoChain: true}).Preflight(plan())

	if clierr.CodeOf(err) != clierr.CodeConflict || !strings.Contains(err.Error(), "no usable chain report") || !strings.Contains(err.Error(), "--drop-validator") {
		t.Fatalf("err = %v, want a conflict: the key a missing report would have named is about to be erased", err)
	}
}

func TestPreflight_aNodeWithoutTheChainUnitCannotBeAValidatorAndIsNeverLookedUp(t *testing.T) {
	w := newWorld()
	w.validator["10.0.0.3"] = true

	if _, err := w.step(Options{Node: "10.0.0.3"}).Preflight(plan()); err != nil {
		t.Fatalf("Preflight: %v", err)
	}

	if len(w.asked) != 0 {
		t.Errorf("the telemetry was read for %v: a node with no chain unit has no validator key, and a removal should not need the gateway to say so", w.asked)
	}
}

func TestPreflight_unreadableValidatorStatusIsNotAssumedToBeNo(t *testing.T) {
	w := newWorld()
	w.probes["10.0.0.3"] = "yes no\n"
	w.valErr = errors.New("gateway down")

	_, err := w.step(Options{Node: "10.0.0.3"}).Preflight(plan())

	if clierr.CodeOf(err) != clierr.CodeUnavailable || !strings.Contains(err.Error(), "--offline") || !strings.Contains(err.Error(), "orama auth login") {
		t.Fatalf("err = %v, want unavailable with the sign-in and --offline hints", err)
	}
}

func TestPreflight_aGlobalLayerNodeNeedsAChainDecision(t *testing.T) {
	w := newWorld()
	w.probes["10.0.0.3"] = "yes yes\n"

	_, err := w.step(Options{Node: "10.0.0.3"}).Preflight(plan())

	if clierr.CodeOf(err) != clierr.CodeUsage || !strings.Contains(err.Error(), "--chain-node-id") || !strings.Contains(err.Error(), "--no-chain") {
		t.Fatalf("err = %v, want a usage error naming both ways out", err)
	}
}

func TestPreflight_noChainLeavesTheRegistrationAndSaysSo(t *testing.T) {
	w := newWorld()
	w.probes["10.0.0.3"] = "yes yes\n"

	steps, err := w.step(Options{Node: "10.0.0.3", NoChain: true}).Preflight(plan())

	if err != nil || len(steps) != 1 || !strings.Contains(steps[0], "bonds stay locked") {
		t.Fatalf("steps = %v, err = %v", steps, err)
	}
	if len(w.opened) != 0 {
		t.Error("--no-chain reached the chain")
	}
}

func TestPreflight_aClusterOnlyNodeHasNothingOnTheChain(t *testing.T) {
	w := newWorld()

	steps, err := w.step(Options{Node: "10.0.0.3"}).Preflight(plan())

	if err != nil || len(steps) != 0 {
		t.Fatalf("steps = %v, err = %v; a node without the global layer has no chain step", steps, err)
	}
}

func TestPreflight_aGoneNodeCannotBeAskedSoItsRegistrationIsLeftAndTheStepSaysSo(t *testing.T) {
	w := newWorld()

	steps, err := w.step(Options{Node: "10.0.0.3", Offline: true}).Preflight(plan())

	if err != nil || len(steps) != 1 || !strings.Contains(steps[0], "is gone") || !strings.Contains(steps[0], "orama global retire") {
		t.Fatalf("steps = %v, err = %v: removing a machine that is already gone must not need a decision about a chain it cannot be asked about", steps, err)
	}
	if len(w.opened) != 0 {
		t.Error("the chain was reached for a node that was not asked to be retired there")
	}
}

func TestPreflight_retiresAnActiveNodeThroughASurvivor(t *testing.T) {
	w := newWorld()
	w.chain = activeNode()
	w.probes["10.0.0.3"] = "yes yes\n"
	step := w.step(Options{Node: "10.0.0.3", ChainNodeID: "node-3"})

	steps, err := step.Preflight(plan())

	if err != nil || len(steps) != 1 || !strings.Contains(steps[0], "retire node node-3 on the chain") {
		t.Fatalf("steps = %v, err = %v", steps, err)
	}
	if w.opened[0][0] != "10.0.0.1" {
		t.Errorf("the chain was reached through %v, want a survivor and not the node about to be erased", w.opened[0])
	}
}

func TestPreflight_anOfflineNodeNeverCarriesTheChainCall(t *testing.T) {
	w := newWorld()
	w.chain = activeNode()
	w.probes = map[string]string{"10.0.0.1": "no no\n", "10.0.0.2": "no no\n", "10.0.0.3": "yes no\n"}

	_, err := w.step(Options{Node: "10.0.0.3", ChainNodeID: "node-3", Offline: true}).Preflight(plan())

	if err == nil || clierr.CodeOf(err) != clierr.CodeUnavailable {
		t.Fatalf("err = %v, want the chain unreachable: no survivor runs it and the target is gone", err)
	}
}

func TestPreflight_unknownNodeIDIsNotFound(t *testing.T) {
	w := newWorld()
	w.chain = nil

	_, err := w.step(Options{Node: "10.0.0.3", ChainNodeID: "typo"}).Preflight(plan())

	if clierr.CodeOf(err) != clierr.CodeNotFound || !strings.Contains(err.Error(), `"typo"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestPreflight_aNodeAlreadyRetiredOnTheChainNeedsNoRetirement(t *testing.T) {
	w := newWorld()
	w.chain = &chainreach.ChainNode{ID: "node-3", Operator: operatorAddr, Status: chainreach.StatusRetired}
	step := w.step(Options{Node: "10.0.0.3", ChainNodeID: "node-3"})

	steps, err := step.Preflight(plan())

	if err != nil || len(steps) != 1 || !strings.Contains(steps[0], "already retired") {
		t.Fatalf("steps = %v, err = %v", steps, err)
	}
	if err := step.Before(plan()); err != nil || len(w.tx.retired) != 0 {
		t.Errorf("Before = %v, retired %v: nothing must be sent for a node that is gone", err, w.tx.retired)
	}
}

func TestPreflight_reservedBytesRefuseBeforeAnythingIsErased(t *testing.T) {
	w := newWorld()
	w.chain = activeNode()
	w.chain.ReservedBytes = 4096

	_, err := w.step(Options{Node: "10.0.0.3", ChainNodeID: "node-3"}).Preflight(plan())

	if clierr.CodeOf(err) != clierr.CodeConflict || !strings.Contains(err.Error(), "4096 bytes") {
		t.Fatalf("err = %v", err)
	}
}

func TestPreflight_anIdThatBelongsToAnotherMachineIsRefused(t *testing.T) {
	w := newWorld()
	w.chain = activeNode()
	w.chain.Endpoints = []string{"10.0.0.1:31000"}

	_, err := w.step(Options{Node: "10.0.0.3", ChainNodeID: "node-3"}).Preflight(plan())

	if clierr.CodeOf(err) != clierr.CodeConflict || !strings.Contains(err.Error(), "none is 10.0.0.3") {
		t.Fatalf("err = %v, want the mistyped id refused: retiring another of the operator's nodes cannot be undone", err)
	}
}

func TestPreflight_aNodeWithNoRegisteredEndpointIsAllowedAndTheStepSaysItCouldNotBeMatched(t *testing.T) {
	w := newWorld()
	w.chain = activeNode()
	w.chain.Endpoints = nil

	steps, err := w.step(Options{Node: "10.0.0.3", ChainNodeID: "node-3"}).Preflight(plan())

	if err != nil || len(steps) != 1 || !strings.Contains(steps[0], "no IPv4 endpoint") {
		t.Fatalf("steps = %v, err = %v", steps, err)
	}
}

func TestPreflight_theStepNamesTheEndpointThatMatched(t *testing.T) {
	w := newWorld()
	w.chain = activeNode()

	steps, err := w.step(Options{Node: "10.0.0.3", ChainNodeID: "node-3"}).Preflight(plan())

	if err != nil || len(steps) != 1 || !strings.Contains(steps[0], "its registered endpoint is 10.0.0.3") {
		t.Fatalf("steps = %v, err = %v", steps, err)
	}
}

func TestBefore_retiresTheNodeWithTheOperatorsWallet(t *testing.T) {
	w := newWorld()
	w.chain = activeNode()
	step := w.step(Options{Node: "10.0.0.3", ChainNodeID: "node-3"})
	if _, err := step.Preflight(plan()); err != nil {
		t.Fatal(err)
	}

	if err := step.Before(plan()); err != nil {
		t.Fatalf("Before: %v", err)
	}

	if !slices.Equal(w.tx.retired, []string{"node-3"}) {
		t.Errorf("retired %v", w.tx.retired)
	}
}

func TestBefore_anotherOperatorsNodeIsRefusedAndNothingIsSent(t *testing.T) {
	w := newWorld()
	w.chain = activeNode()
	w.tx.operator = "orama1someoneelse"
	step := w.step(Options{Node: "10.0.0.3", ChainNodeID: "node-3"})
	if _, err := step.Preflight(plan()); err != nil {
		t.Fatal(err)
	}

	err := step.Before(plan())

	if clierr.CodeOf(err) != clierr.CodeConflict || !strings.Contains(err.Error(), "only its operator can retire it") {
		t.Fatalf("err = %v", err)
	}
	if len(w.tx.retired) != 0 {
		t.Errorf("sent %v for a node the wallet does not own", w.tx.retired)
	}
}

func TestBefore_aLockedWalletStopsTheRemovalWithNothingChanged(t *testing.T) {
	w := newWorld()
	w.chain = activeNode()
	w.tx.operatorEr = errors.New("agent is locked")
	step := w.step(Options{Node: "10.0.0.3", ChainNodeID: "node-3"})
	if _, err := step.Preflight(plan()); err != nil {
		t.Fatal(err)
	}

	err := step.Before(plan())

	if clierr.CodeOf(err) != clierr.CodeAuth || !strings.Contains(err.Error(), "Nothing was removed") {
		t.Fatalf("err = %v", err)
	}
}

func TestBefore_aRefusedRetirementStopsTheRemoval(t *testing.T) {
	w := newWorld()
	w.chain = activeNode()
	w.tx.retireErr = errors.New("insufficient fee")
	step := w.step(Options{Node: "10.0.0.3", ChainNodeID: "node-3"})
	if _, err := step.Preflight(plan()); err != nil {
		t.Fatal(err)
	}

	err := step.Before(plan())

	if err == nil || !strings.Contains(err.Error(), "insufficient fee") || !strings.Contains(err.Error(), "Nothing was removed") {
		t.Fatalf("err = %v", err)
	}
}

func TestOptions_validate(t *testing.T) {
	if err := (Options{}).validate(); clierr.CodeOf(err) != clierr.CodeUsage {
		t.Errorf("no node: %v", err)
	}
	if err := (Options{Node: "1.2.3.4", NoChain: true, ChainNodeID: "x"}).validate(); clierr.CodeOf(err) != clierr.CodeUsage {
		t.Errorf("contradiction: %v", err)
	}
	if err := (Options{Node: "1.2.3.4", NoChain: true}).validate(); err != nil {
		t.Errorf("valid options refused: %v", err)
	}
}

func TestStatusWord(t *testing.T) {
	if got := statusWord("NODE_STATUS_RETIRED"); got != "retired" {
		t.Errorf("statusWord = %q", got)
	}
}

func TestPreflight_theWalletIsPinnedToTheChainOfTheEnvironmentBeforeAnythingIsReached(t *testing.T) {
	w := newWorld()
	w.chain = activeNode()
	w.probes["10.0.0.3"] = "yes yes\n"
	step := w.step(Options{Node: "10.0.0.3", ChainNodeID: "node-3", ChainID: "orama-stagenet-1"})

	if _, err := step.Preflight(plan()); err != nil {
		t.Fatal(err)
	}
	if len(w.pinned) != 1 || w.pinned[0] != "stagenet/orama-stagenet-1" {
		t.Errorf("the pin was asked for %v: it is the environment's and the --chain-id", w.pinned)
	}
	if len(w.checked) != 1 || w.checked[0] != "orama-stagenet-1" {
		t.Errorf("the node's chain was checked against %v", w.checked)
	}
}

func TestPreflight_aNetworkWithNoKnownChainIDStopsBeforeTheChainIsReached(t *testing.T) {
	w := newWorld()
	w.chain = activeNode()
	w.probes["10.0.0.3"] = "yes yes\n"
	w.pinErr = clierr.Usage("cannot tell which chain %q runs: pass --chain-id <id>", "stagenet")

	_, err := w.step(Options{Node: "10.0.0.3", ChainNodeID: "node-3"}).Preflight(plan())

	if clierr.CodeOf(err) != clierr.CodeUsage || !strings.Contains(err.Error(), "--chain-id") {
		t.Fatalf("err = %v", err)
	}
	if len(w.opened) != 0 {
		t.Errorf("the chain was reached (%v) although the wallet could not be pinned", w.opened)
	}
}

func TestPreflight_aNodeOnAnotherChainThanTheNetworksIsRefusedBeforeAnythingIsSigned(t *testing.T) {
	w := newWorld()
	w.chain = activeNode()
	w.probes["10.0.0.3"] = "yes yes\n"
	w.checkErr = errors.New(`10.0.0.1 runs the chain "orama-evil-1", not the "orama-stagenet-1" this network runs: refusing to sign for it`)

	_, err := w.step(Options{Node: "10.0.0.3", ChainNodeID: "node-3"}).Preflight(plan())

	if err == nil || !strings.Contains(err.Error(), "refusing to sign") || !strings.Contains(err.Error(), "Nothing was removed") {
		t.Fatalf("err = %v", err)
	}
	if len(w.tx.retired) != 0 {
		t.Errorf("a retirement was signed: %v", w.tx.retired)
	}
}
