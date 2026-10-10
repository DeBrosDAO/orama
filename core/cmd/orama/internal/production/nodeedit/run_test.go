package nodeedit

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/production/chainreach"
	"github.com/DeBrosOfficial/network/pkg/clusterreg"
	"github.com/DeBrosOfficial/network/pkg/inspector"
	"github.com/DeBrosOfficial/network/pkg/onchain"
)

const walletAddr = "orama1operator"

type fakeTx struct {
	operator   string
	declareErr error
	declared   []clusterreg.Capacity
}

func (f *fakeTx) Operator(context.Context) (string, error) { return f.operator, nil }

func (f *fakeTx) DeclareCapacity(_ context.Context, c clusterreg.Capacity) (*onchain.Receipt, error) {
	if f.declareErr != nil {
		return nil, f.declareErr
	}
	f.declared = append(f.declared, c)
	return &onchain.Receipt{Height: 9}, nil
}

type harness struct {
	state     NodeState
	chainNode *chainreach.ChainNode
	tx        *fakeTx
	editErr   error
	edited    []string
	opened    [][]string
	formCalls int
	formReply Settings
}

func newHarness() *harness {
	return &harness{
		state:     fullNode,
		chainNode: &chainreach.ChainNode{ID: "node-1", Operator: walletAddr, Status: "NODE_STATUS_ACTIVE"},
		tx:        &fakeTx{operator: walletAddr},
	}
}

func (h *harness) runner(opts Options) (*runner, *bytes.Buffer) {
	var out bytes.Buffer
	opts.Out = &out
	if opts.In == nil {
		opts.In = strings.NewReader("yes\n")
	}
	d := &declarer{
		runner: chainreach.Runner{
			Output: func(inspector.Node, string) (string, error) { return "yes yes\n", nil },
			Tunnel: func(_ context.Context, n inspector.Node, _ string) (string, func() error, error) {
				h.opened = append(h.opened, []string{n.Host})
				return "127.0.0.1:1", func() error { return nil }, nil
			},
		},
		readNode: func(context.Context, *chainreach.Reach, string) (*chainreach.ChainNode, error) {
			return h.chainNode, nil
		},
		newClient: func(context.Context, *chainreach.Reach) (txClient, error) { return h.tx, nil },
	}
	nodes := []inspector.Node{{Host: "10.0.0.1", User: "root"}, {Host: "10.0.0.2", User: "root"}}
	return &runner{opts: opts, nodes: nodes, seams: seams{
		state:      func(inspector.Node) (NodeState, error) { return h.state, nil },
		chooseNode: func(hosts []string) (string, error) { return hosts[1], nil },
		form: func(string, NodeState) (Settings, string, error) {
			h.formCalls++
			return h.formReply, "node-1", nil
		},
		editNode: func(n inspector.Node, p *Plan) error {
			h.edited = append(h.edited, n.Host+" "+editArgs(p))
			return h.editErr
		},
		declarer: d,
	}}, &out
}

func TestRun_storageChangeDeclaresOnTheChainThenResizesTheNode(t *testing.T) {
	h := newHarness()
	r, out := h.runner(Options{Node: "10.0.0.1", Settings: Settings{StorageGB: gb(100)}, ChainNodeID: "node-1"})

	if err := r.run(context.Background()); err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}

	if len(h.tx.declared) != 1 || h.tx.declared[0].NodeID != "node-1" || h.tx.declared[0].Bytes != 100_000_000_000 {
		t.Errorf("declared %+v, want 100 GB for node-1", h.tx.declared)
	}
	if len(h.edited) != 1 || h.edited[0] != "10.0.0.1 maint global edit --storage-gb 100" {
		t.Errorf("edited %v", h.edited)
	}
	if h.opened[0][0] != "10.0.0.1" {
		t.Errorf("the chain was reached through %v, want the edited node first", h.opened[0])
	}
}

func TestRun_exitChangeNeverTouchesTheChain(t *testing.T) {
	h := newHarness()
	r, _ := h.runner(Options{Node: "10.0.0.1", Settings: Settings{Exit: flag(true)}, Yes: true})

	if err := r.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}

	if len(h.opened) != 0 || len(h.tx.declared) != 0 {
		t.Errorf("the chain was used for a relay role: %v %v", h.opened, h.tx.declared)
	}
	if len(h.edited) != 1 || h.edited[0] != "10.0.0.1 maint global edit --exit=true" {
		t.Errorf("edited %v", h.edited)
	}
}

func TestRun_bothChangesAreOneNodeEdit(t *testing.T) {
	h := newHarness()
	r, _ := h.runner(Options{Node: "10.0.0.1", Settings: Settings{StorageGB: gb(100), Exit: flag(true)}, ChainNodeID: "node-1", Yes: true})

	if err := r.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}

	if len(h.edited) != 1 || h.edited[0] != "10.0.0.1 maint global edit --storage-gb 100 --exit=true" {
		t.Errorf("edited %v, want a single node-side call", h.edited)
	}
}

func TestRun_declinedChangesNothing(t *testing.T) {
	h := newHarness()
	r, _ := h.runner(Options{Node: "10.0.0.1", Settings: Settings{StorageGB: gb(100)}, ChainNodeID: "node-1", In: strings.NewReader("no\n")})

	err := r.run(context.Background())

	if clierr.CodeOf(err) != clierr.CodeAborted {
		t.Fatalf("err = %v", err)
	}
	if len(h.tx.declared) != 0 || len(h.edited) != 0 {
		t.Errorf("declared %v, edited %v after the operator declined", h.tx.declared, h.edited)
	}
}

func TestRun_nothingToChangeSaysSoAndSendsNothing(t *testing.T) {
	h := newHarness()
	r, out := h.runner(Options{Node: "10.0.0.1", Settings: Settings{StorageGB: gb(50)}, ChainNodeID: "node-1"})

	if err := r.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}

	if len(h.tx.declared)+len(h.edited) != 0 || !strings.Contains(out.String(), "nothing to do") {
		t.Errorf("declared %v, edited %v, output:\n%s", h.tx.declared, h.edited, out)
	}
}

func TestRun_theChainRefusingStopsBeforeTheNodeIsTouched(t *testing.T) {
	h := newHarness()
	h.tx.declareErr = errors.New("capacity above what the bond backs")
	r, _ := h.runner(Options{Node: "10.0.0.1", Settings: Settings{StorageGB: gb(100)}, ChainNodeID: "node-1", Yes: true})

	err := r.run(context.Background())

	if err == nil || !strings.Contains(err.Error(), "capacity above what the bond backs") {
		t.Fatalf("err = %v", err)
	}
	if len(h.edited) != 0 {
		t.Errorf("the node was resized (%v) though the chain refused the capacity", h.edited)
	}
}

func TestRun_aNodeFailureAfterTheChainSaysWhatStateItIsIn(t *testing.T) {
	h := newHarness()
	h.editErr = errors.New("ssh timeout")
	r, _ := h.runner(Options{Node: "10.0.0.1", Settings: Settings{StorageGB: gb(100)}, ChainNodeID: "node-1", Yes: true})

	err := r.run(context.Background())

	if err == nil || !strings.Contains(err.Error(), "the chain now declares 100 GB") || !strings.Contains(err.Error(), "ssh timeout") {
		t.Fatalf("err = %v, want the half-done state spelled out", err)
	}
}

func TestRun_anotherOperatorsNodeIsRefused(t *testing.T) {
	h := newHarness()
	h.chainNode.Operator = "orama1someoneelse"
	r, _ := h.runner(Options{Node: "10.0.0.1", Settings: Settings{StorageGB: gb(100)}, ChainNodeID: "node-1", Yes: true})

	err := r.run(context.Background())

	if clierr.CodeOf(err) != clierr.CodeConflict || len(h.tx.declared) != 0 || len(h.edited) != 0 {
		t.Fatalf("err = %v, declared %v, edited %v", err, h.tx.declared, h.edited)
	}
}

func TestRun_capacityBelowWhatDealsReserveIsRefusedBeforeConfirming(t *testing.T) {
	h := newHarness()
	h.chainNode.ReservedBytes = 200_000_000_000
	r, _ := h.runner(Options{Node: "10.0.0.1", Settings: Settings{StorageGB: gb(100)}, ChainNodeID: "node-1", In: strings.NewReader("")})

	err := r.run(context.Background())

	if clierr.CodeOf(err) != clierr.CodeConflict || !strings.Contains(err.Error(), "reserve 200000000000 bytes") {
		t.Fatalf("err = %v", err)
	}
}

func TestRun_unknownChainNodeIsNotFound(t *testing.T) {
	h := newHarness()
	h.chainNode = nil
	r, _ := h.runner(Options{Node: "10.0.0.1", Settings: Settings{StorageGB: gb(100)}, ChainNodeID: "typo", Yes: true})

	if err := r.run(context.Background()); clierr.CodeOf(err) != clierr.CodeNotFound {
		t.Fatalf("err = %v", err)
	}
}

func TestRun_noNodeGivenAsksForOneAndTheFormFillsTheSettings(t *testing.T) {
	h := newHarness()
	h.formReply = Settings{Exit: flag(true)}
	r, _ := h.runner(Options{Interactive: true, Yes: true})

	if err := r.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}

	if h.formCalls != 1 || len(h.edited) != 1 || !strings.HasPrefix(h.edited[0], "10.0.0.2 ") {
		t.Errorf("form calls %d, edited %v: the form should have picked 10.0.0.2 and edited it", h.formCalls, h.edited)
	}
}

func TestRun_aNodeOutsideTheNetworkIsNotFound(t *testing.T) {
	h := newHarness()
	r, _ := h.runner(Options{Node: "203.0.113.99", Settings: Settings{Exit: flag(true)}})

	if err := r.run(context.Background()); clierr.CodeOf(err) != clierr.CodeNotFound {
		t.Fatalf("err = %v", err)
	}
}

func TestRun_refusedGlobalChangeTouchesNothing(t *testing.T) {
	h := newHarness()
	r, _ := h.runner(Options{Node: "10.0.0.1", Settings: Settings{Global: flag(false)}, Yes: true})

	if err := r.run(context.Background()); clierr.CodeOf(err) != clierr.CodeConflict {
		t.Fatalf("err = %v", err)
	}
	if len(h.edited)+len(h.tx.declared) != 0 {
		t.Error("a refused change was partly applied")
	}
}

func TestEditArgs(t *testing.T) {
	if got := editArgs(&Plan{Storage: gb(7)}); got != "maint global edit --storage-gb 7" {
		t.Errorf("args = %q", got)
	}
	if got := editArgs(&Plan{Exit: flag(false)}); got != "maint global edit --exit=false" {
		t.Errorf("args = %q", got)
	}
}
