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
	pinned    []string
	checked   []string
	pinErr    error
	checkErr  error
}

func newHarness() *harness {
	return &harness{
		state:     fullNode,
		chainNode: &chainreach.ChainNode{ID: "node-1", Operator: walletAddr, Status: "NODE_STATUS_ACTIVE", Endpoints: []string{"10.0.0.1:31000"}},
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
		newClient: func(context.Context, *chainreach.Reach, string) (txClient, error) { return h.tx, nil },
		pin: func(env, explicit string) (string, error) {
			h.pinned = append(h.pinned, env+"/"+explicit)
			return "orama-stagenet-1", h.pinErr
		},
		checkChain: func(_ context.Context, _ *chainreach.Reach, want string) error {
			h.checked = append(h.checked, want)
			return h.checkErr
		},
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

	if err == nil || !strings.Contains(err.Error(), "the chain now declares 100 GB") || !strings.Contains(err.Error(), "ssh timeout") || !strings.Contains(err.Error(), "partly changed") {
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

func TestRun_noNodeOutsideATerminalIsAUsageError(t *testing.T) {
	h := newHarness()
	r, _ := h.runner(Options{Settings: Settings{Exit: flag(true)}, Yes: true})

	err := r.run(context.Background())

	if clierr.CodeOf(err) != clierr.CodeUsage || !strings.Contains(err.Error(), "--node is required") {
		t.Fatalf("err = %v", err)
	}
	if len(h.edited) != 0 {
		t.Errorf("edited %v without a node", h.edited)
	}
}

func TestRun_anIdThatBelongsToAnotherMachineIsRefusedBeforeConfirming(t *testing.T) {
	h := newHarness()
	h.chainNode.Endpoints = []string{"10.0.0.2:31000"}
	r, _ := h.runner(Options{Node: "10.0.0.1", Settings: Settings{StorageGB: gb(100)}, ChainNodeID: "node-1", In: strings.NewReader("")})

	err := r.run(context.Background())

	if clierr.CodeOf(err) != clierr.CodeConflict || !strings.Contains(err.Error(), "none is 10.0.0.1") {
		t.Fatalf("err = %v, want the mistyped id refused: declaring another node's capacity is not what the operator asked for", err)
	}
	if len(h.tx.declared)+len(h.edited) != 0 {
		t.Errorf("declared %v, edited %v", h.tx.declared, h.edited)
	}
}

func TestRun_aNodeEditFailureWithoutTheChainSaysItMayBePartlyChanged(t *testing.T) {
	h := newHarness()
	h.editErr = errors.New("ssh timeout")
	r, _ := h.runner(Options{Node: "10.0.0.1", Settings: Settings{Exit: flag(true)}, Yes: true})

	err := r.run(context.Background())

	if err == nil || !strings.Contains(err.Error(), "may be partly changed") || !strings.Contains(err.Error(), "ssh timeout") {
		t.Fatalf("err = %v", err)
	}
}

func TestRun_theDeclarationIsSignedOnlyForTheChainTheNetworkRuns(t *testing.T) {
	h := newHarness()
	r, out := h.runner(Options{Env: "stagenet", Node: "10.0.0.1", Settings: Settings{StorageGB: gb(100)}, ChainNodeID: "node-1", ChainID: "orama-stagenet-1"})

	if err := r.run(context.Background()); err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if len(h.pinned) != 1 || h.pinned[0] != "stagenet/orama-stagenet-1" {
		t.Errorf("pinned %v: the environment and --chain-id", h.pinned)
	}
	if len(h.checked) != 1 || h.checked[0] != "orama-stagenet-1" {
		t.Errorf("the node's chain was checked against %v", h.checked)
	}
}

func TestRun_aNodeOnAnotherChainChangesNothing(t *testing.T) {
	h := newHarness()
	h.checkErr = errors.New(`10.0.0.1 runs the chain "orama-evil-1", not the "orama-stagenet-1" this network runs: refusing to sign for it`)
	r, out := h.runner(Options{Node: "10.0.0.1", Settings: Settings{StorageGB: gb(100)}, ChainNodeID: "node-1"})

	err := r.run(context.Background())

	if err == nil || !strings.Contains(err.Error(), "refusing to sign") {
		t.Fatalf("err = %v\n%s", err, out)
	}
	if len(h.tx.declared) != 0 || len(h.edited) != 0 {
		t.Errorf("declared %v, edited %v: nothing may change", h.tx.declared, h.edited)
	}
}

func TestRun_aNetworkWithNoKnownChainIDNeedsTheFlag(t *testing.T) {
	h := newHarness()
	h.pinErr = clierr.Usage("cannot tell which chain %q runs: pass --chain-id <id>", "stagenet")
	r, _ := h.runner(Options{Node: "10.0.0.1", Settings: Settings{StorageGB: gb(100)}, ChainNodeID: "node-1"})

	err := r.run(context.Background())

	if clierr.CodeOf(err) != clierr.CodeUsage || !strings.Contains(err.Error(), "--chain-id") {
		t.Fatalf("err = %v", err)
	}
	if len(h.opened) != 0 || len(h.tx.declared) != 0 {
		t.Errorf("the chain was reached (%v) or a capacity declared (%v)", h.opened, h.tx.declared)
	}
}

func TestRun_anExitRelayWithoutTheExitRoleOnTheChainIsWarnedAboutAndStillEdited(t *testing.T) {
	h := newHarness()
	h.chainNode.Roles = []string{"ROLE_STORAGE", "ROLE_RELAY"}
	r, out := h.runner(Options{Node: "10.0.0.1", Settings: Settings{Exit: flag(true)}, ChainNodeID: "node-1", Yes: true})

	if err := r.run(context.Background()); err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}

	if !strings.Contains(out.String(), "holds no exit role on the chain") || !strings.Contains(out.String(), "orama global bond --role exit --id node-1") {
		t.Errorf("no warning that says what to do:\n%s", out)
	}
	if len(h.edited) != 1 || len(h.tx.declared) != 0 {
		t.Errorf("a warning stops nothing and sends nothing: edited %v declared %v", h.edited, h.tx.declared)
	}
}

func TestRun_aPlainRelayThatStillHoldsTheExitRoleOnTheChainIsWarnedAbout(t *testing.T) {
	h := newHarness()
	h.chainNode.Roles = []string{"ROLE_STORAGE", "ROLE_RELAY", "ROLE_EXIT"}
	h.state = NodeState{Global: true, IPFS: true, Relay: true, Exit: true, StorageMax: "55GB"}
	r, out := h.runner(Options{Node: "10.0.0.1", Settings: Settings{Exit: flag(false)}, ChainNodeID: "node-1", Yes: true})

	if err := r.run(context.Background()); err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}

	if !strings.Contains(out.String(), "still holds the exit role on the chain") || !strings.Contains(out.String(), "orama global unbond --role exit --id node-1") {
		t.Errorf("no warning that says what to do:\n%s", out)
	}
}

func TestRun_anExitPolicyThatAgreesWithTheChainSaysNothing(t *testing.T) {
	h := newHarness()
	h.chainNode.Roles = []string{"ROLE_RELAY", "ROLE_EXIT"}
	r, out := h.runner(Options{Node: "10.0.0.1", Settings: Settings{Exit: flag(true)}, ChainNodeID: "node-1", Yes: true})

	if err := r.run(context.Background()); err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}

	if strings.Contains(out.String(), "!") {
		t.Errorf("a warning where the relay policy and the chain agree:\n%s", out)
	}
}

func TestRun_anExitEditWithoutAChainNodeIDSaysItDidNotCompare(t *testing.T) {
	h := newHarness()
	r, out := h.runner(Options{Node: "10.0.0.1", Settings: Settings{Exit: flag(true)}, Yes: true})

	if err := r.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}

	if !strings.Contains(out.String(), "was not compared") || !strings.Contains(out.String(), "--chain-node-id") {
		t.Errorf("output:\n%s", out)
	}
	if len(h.opened) != 0 {
		t.Errorf("the chain was reached without a node id: %v", h.opened)
	}
}

// An edit that changes the exit policy and the capacity opens the chain once: the exit-role check and
// the capacity preflight share one connection, which close ends. A second Open used to replace the
// first, whose tunnels were never closed.
func TestRun_exitAndCapacityShareOneChainConnection(t *testing.T) {
	storageOnly := newHarness()
	r, _ := storageOnly.runner(Options{Node: "10.0.0.1", Settings: Settings{StorageGB: gb(100)}, ChainNodeID: "node-1", Yes: true})
	if err := r.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	both := newHarness()
	r, _ = both.runner(Options{Node: "10.0.0.1", Settings: Settings{StorageGB: gb(100), Exit: flag(true)}, ChainNodeID: "node-1", Yes: true})
	if err := r.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(both.opened) != len(storageOnly.opened) || len(both.opened) == 0 {
		t.Fatalf("exit + capacity opened %d tunnels, capacity alone %d: one chain connection must serve both", len(both.opened), len(storageOnly.opened))
	}
}
