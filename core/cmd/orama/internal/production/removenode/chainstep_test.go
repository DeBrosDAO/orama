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
	valErr    error
	chain     *chainreach.ChainNode
	openErr   error
	opened    [][]string
	tx        *fakeTx
	clientErr error
}

func newWorld() *world {
	return &world{
		probes:    map[string]string{"10.0.0.1": "yes no\n", "10.0.0.2": "yes no\n", "10.0.0.3": "no no\n"},
		validator: map[string]bool{},
		tx:        &fakeTx{operator: operatorAddr},
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
		validator: func(_ context.Context, _, host string) (bool, error) { return w.validator[host], w.valErr },
		readNode: func(context.Context, *chainreach.Reach, string) (*chainreach.ChainNode, error) {
			return w.chain, nil
		},
		newClient: func(context.Context, *chainreach.Reach) (txClient, error) { return w.tx, w.clientErr },
	}
}

func plan() *decommission.Plan {
	nodes := []inspector.Node{{Host: "10.0.0.1"}, {Host: "10.0.0.2"}, {Host: "10.0.0.3"}}
	return &decommission.Plan{Nodes: nodes, Target: nodes[2]}
}

func activeNode() *chainreach.ChainNode {
	return &chainreach.ChainNode{ID: "node-3", Operator: operatorAddr, Status: "NODE_STATUS_ACTIVE"}
}

func TestPreflight_aNodeInTheValidatorSetIsNotErasedByAccident(t *testing.T) {
	w := newWorld()
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
		w.validator["10.0.0.3"] = true

		if _, err := w.step(opts).Preflight(plan()); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestPreflight_unreadableValidatorStatusIsNotAssumedToBeNo(t *testing.T) {
	w := newWorld()
	w.valErr = errors.New("gateway down")

	_, err := w.step(Options{Node: "10.0.0.3"}).Preflight(plan())

	if clierr.CodeOf(err) != clierr.CodeUnavailable || !strings.Contains(err.Error(), "--offline") {
		t.Fatalf("err = %v, want unavailable with the --offline hint", err)
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

func TestPreflight_aGoneNodeCannotBeAskedSoItMustBeToldWhatToDo(t *testing.T) {
	w := newWorld()

	_, err := w.step(Options{Node: "10.0.0.3", Offline: true}).Preflight(plan())

	if clierr.CodeOf(err) != clierr.CodeUsage || !strings.Contains(err.Error(), "is gone") {
		t.Fatalf("err = %v", err)
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
