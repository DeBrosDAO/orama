package nodeedit

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/DeBrosOfficial/network/cmd/orama/internal"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/noderesolver"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/production/upgrade"
	"github.com/DeBrosOfficial/network/pkg/inspector"
	"github.com/DeBrosOfficial/network/pkg/remotessh"
)

// Options are the flags of `orama edit`.
type Options struct {
	// Env is the network; empty is the active one.
	Env string
	// Node is the public IP of the node to edit; empty asks in the form.
	Node string
	// Settings are the changes asked for by flag. None opens the form.
	Settings Settings
	// ChainNodeID names the node on the chain for a storage change; NoChain
	// resizes the node only.
	ChainNodeID string
	NoChain     bool
	// Yes skips the confirmation.
	Yes bool
	// Interactive is true when the form may be used: input and output are a terminal.
	Interactive bool
	In          io.Reader
	Out         io.Writer
}

// seams are what a test replaces: the nodes, the form and the node-side edit.
type seams struct {
	state      func(node inspector.Node) (NodeState, error)
	chooseNode func(hosts []string) (string, error)
	form       func(host string, st NodeState) (Settings, string, error)
	editNode   func(node inspector.Node, p *Plan) error
	declarer   *declarer
}

// Run is `orama edit`.
func Run(ctx context.Context, opts Options) error {
	env := opts.Env
	if env == "" {
		active, err := cli.GetActiveEnvironment()
		if err != nil {
			return clierr.Usage("no --env given and no active network: %v", err)
		}
		env = active.Name
	}
	if opts.NoChain && opts.ChainNodeID != "" {
		return clierr.Usage("--no-chain and --chain-node-id contradict each other: either the capacity is declared on the chain or it is not")
	}
	if opts.Settings.Empty() && !opts.Interactive {
		return clierr.Usage("name what to change (--storage-gb, --exit or --global), or run `orama edit` in a terminal for the form")
	}
	nodes, err := noderesolver.ResolveNodes(env)
	if err != nil {
		return err
	}
	cleanup, err := remotessh.PrepareNodeKeys(nodes)
	if err != nil {
		return err
	}
	defer cleanup()
	r := &runner{opts: opts, nodes: nodes, seams: defaultSeams()}
	defer r.seams.declarer.close()
	return r.run(ctx)
}

func defaultSeams() seams {
	return seams{state: readState, chooseNode: chooseNodeForm, form: editForm, editNode: editOnNode, declarer: newDeclarer()}
}

type runner struct {
	opts  Options
	nodes []inspector.Node
	seams seams
}

func (r *runner) run(ctx context.Context) error {
	out := r.opts.Out
	if out == nil {
		out = os.Stdout
	}
	node, err := r.pickNode()
	if err != nil {
		return err
	}
	st, err := r.seams.state(node)
	if err != nil {
		return clierr.Unavailable("%v", err)
	}
	settings, chainID := r.opts.Settings, r.opts.ChainNodeID
	if settings.Empty() {
		if settings, chainID, err = r.seams.form(node.Host, st); err != nil {
			return err
		}
	}
	plan, err := BuildPlan(node.Host, st, settings, chainID, r.opts.NoChain)
	if err != nil {
		return err
	}
	plan.Render(out)
	if plan.Nothing() {
		return nil
	}
	if plan.DeclareOnChain {
		if err := r.seams.declarer.preflight(ctx, chainID, node.Host, *plan.Storage*bytesPerGB, r.candidates(node)); err != nil {
			return err
		}
	}
	if !r.opts.Yes {
		fmt.Fprint(out, "\nType 'yes' to apply: ")
		if err := clierr.Confirm(r.input(), "yes"); err != nil {
			fmt.Fprintln(out, "Aborted.")
			return err
		}
	}
	return r.apply(ctx, node, plan, chainID, out)
}

// apply makes the changes: the chain first, since the chain is the one that can
// refuse a capacity, then the node.
func (r *runner) apply(ctx context.Context, node inspector.Node, plan *Plan, chainID string, out io.Writer) error {
	if plan.DeclareOnChain {
		height, err := r.seams.declarer.declare(ctx, *plan.Storage*bytesPerGB)
		if err != nil {
			return err
		}
		fmt.Fprintln(out, declaredMessage(chainID, *plan.Storage, height))
	}
	if err := r.seams.editNode(node, plan); err != nil {
		if plan.DeclareOnChain {
			return clierr.Failure("the chain now declares %d GB for %s, but the edit of %s failed and it may be partly changed: %v\n  Run the same command again: declaring the same capacity is a no-op, and the node is changed this time",
				*plan.Storage, chainID, node.Host, err)
		}
		return clierr.Failure("the edit of %s failed and it may be partly changed: %v\n  Run the same command again once the cause is fixed", node.Host, err)
	}
	fmt.Fprintf(out, "  ✓ %s edited\n", node.Host)
	return nil
}

// pickNode is the node named by --node, or the one chosen in the form.
func (r *runner) pickNode() (inspector.Node, error) {
	host := r.opts.Node
	if host == "" && !r.opts.Interactive {
		return inspector.Node{}, clierr.Usage("--node is required outside a terminal: the form that asks which node needs one")
	}
	if host == "" {
		hosts := make([]string, len(r.nodes))
		for i, n := range r.nodes {
			hosts[i] = n.Host
		}
		var err error
		if host, err = r.seams.chooseNode(hosts); err != nil {
			return inspector.Node{}, err
		}
	}
	found := remotessh.FilterByIP(r.nodes, host)
	if len(found) == 0 {
		return inspector.Node{}, clierr.NotFound("node %s is not in this network", host)
	}
	return found[0], nil
}

// candidates are the nodes whose chain can carry the declaration: the edited
// node first, then the others.
func (r *runner) candidates(node inspector.Node) []inspector.Node {
	out := []inspector.Node{node}
	for _, n := range r.nodes {
		if n.Host != node.Host {
			out = append(out, n)
		}
	}
	return out
}

func (r *runner) input() io.Reader {
	if r.opts.In != nil {
		return r.opts.In
	}
	return os.Stdin
}

// readState asks the node what it has of the global layer.
func readState(node inspector.Node) (NodeState, error) {
	out, err := remotessh.RunSSHOutput(node, remotessh.ScriptCommand(remotessh.SudoPrefix(node), stateScript))
	if err != nil {
		return NodeState{}, fmt.Errorf("read the state of %s: %w", node.Host, err)
	}
	return parseState(out)
}

// editArgs are the arguments of the node's `maint global edit` for the plan.
func editArgs(p *Plan) string {
	args := "maint global edit"
	if p.Storage != nil {
		args += fmt.Sprintf(" --storage-gb %d", *p.Storage)
	}
	if p.Exit != nil {
		args += fmt.Sprintf(" --exit=%t", *p.Exit)
	}
	return args
}

// editOnNode runs the node's own CLI (the staged one, behind the rolling
// upgrade's guard) with the changes. An older node that does not know the
// command says so, and the fix is to upgrade it.
func editOnNode(node inspector.Node, p *Plan) error {
	err := remotessh.RunSSHStreaming(node, upgrade.StagedCLICommand(remotessh.SudoPrefix(node), editArgs(p)))
	if err != nil {
		return fmt.Errorf("%w (a node on a release without `orama maint global edit` needs `orama upgrade --node %s` first)", err, node.Host)
	}
	return nil
}
