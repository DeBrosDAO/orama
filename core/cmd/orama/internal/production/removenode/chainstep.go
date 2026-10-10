package removenode

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/production/chainreach"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/production/decommission"
	"github.com/DeBrosOfficial/network/pkg/inspector"
	"github.com/DeBrosOfficial/network/pkg/onchain"
	"github.com/DeBrosOfficial/network/pkg/rwagent"
)

// defaultSigner is the operator's RootWallet, at the agent socket $RW_AGENT_SOCK
// or its default.
func defaultSigner() *rwagent.Client { return rwagent.New(os.Getenv("RW_AGENT_SOCK")) }

// chainStep is the decommission.Extension that retires the node on the chain.
type chainStep struct {
	ctx  context.Context
	env  string
	opts Options

	runner    chainreach.Runner
	validator func(ctx context.Context, env, host string) (isValidator, known bool, err error)
	readNode  func(ctx context.Context, r *chainreach.Reach, id string) (*chainreach.ChainNode, error)
	newClient func(ctx context.Context, r *chainreach.Reach) (txClient, error)

	reach *chainreach.Reach
	node  *chainreach.ChainNode
}

// txClient is the operator's transaction client (onchain.Client).
type txClient interface {
	Operator(ctx context.Context) (string, error)
	RetireNode(ctx context.Context, nodeID string) (*onchain.Receipt, error)
}

func newChainStep(ctx context.Context, env string, opts Options) *chainStep {
	return &chainStep{
		ctx: ctx, env: env, opts: opts,
		runner: chainreach.SSH(), validator: nodeIsValidator,
		readNode: func(ctx context.Context, r *chainreach.Reach, id string) (*chainreach.ChainNode, error) {
			return r.ChainNode(ctx, id)
		},
		newClient: func(ctx context.Context, r *chainreach.Reach) (txClient, error) {
			return r.Client(ctx, defaultSigner())
		},
	}
}

// close releases the connection to the chain.
func (s *chainStep) close() {
	if s.reach != nil {
		_ = s.reach.Close()
	}
}

// Preflight refuses what must not be removed unattended and returns the chain
// step, if any.
func (s *chainStep) Preflight(p *decommission.Plan) ([]string, error) {
	hasGlobal, err := s.targetHasGlobalLayer(p.Target)
	if err != nil {
		return nil, err
	}
	if err := s.checkValidator(p.Target, hasGlobal); err != nil {
		return nil, err
	}
	switch {
	case s.opts.NoChain && hasGlobal:
		return []string{fmt.Sprintf("leave %s's chain registration alone (--no-chain): its bonds stay locked until you retire the node yourself with 'orama global retire'", p.Target.Host)}, nil
	case s.opts.NoChain:
		return nil, nil
	case s.opts.ChainNodeID == "" && hasGlobal:
		return nil, clierr.Usage("%s runs the global layer, so it may be registered on the chain with a bond.\n"+
			"  Retire it there too: --chain-node-id <the node's id in x/nodes> (orama chain node <id> shows it)\n"+
			"  Or leave its registration alone: --no-chain", p.Target.Host)
	case s.opts.ChainNodeID == "" && s.opts.Offline:
		return []string{fmt.Sprintf("%s is gone, so it cannot be asked whether it was registered on the chain: its registration, if it has one, is left alone, and "+
			"'orama global retire' (or --chain-node-id here) retires it", p.Target.Host)}, nil
	case s.opts.ChainNodeID == "":
		return nil, nil
	}
	return s.planRetire(p)
}

// checkValidator refuses to erase a node that signs for the validator set, and
// one that might: only a node with the chain unit can be a validator, and for
// that node the cluster's telemetry has to say it is not. A missing report is no
// answer, because the key it would have named is about to be erased.
func (s *chainStep) checkValidator(target inspector.Node, hasChain bool) error {
	if s.opts.Offline || s.opts.DropValidator || !hasChain {
		return nil
	}
	isValidator, known, err := s.validator(s.ctx, s.env, target.Host)
	switch {
	case err != nil:
		return clierr.Unavailable("could not read whether %s is in the validator set from the cluster's telemetry: %v\n"+
			"  Sign in with 'orama auth login' and try again; if the machine is gone, pass --offline", target.Host, err)
	case !known:
		return clierr.Conflict("%s runs the chain, and the cluster's telemetry has no usable chain report from it to say whether it signs for the validator set. "+
			"Erasing a validator destroys its consensus key and jails it.\n  Wait for the node to report ('orama status'), or pass --drop-validator to remove it anyway", target.Host)
	case isValidator:
		return clierr.Conflict("%s signs blocks for the validator set. Erasing it destroys the validator's consensus key and the "+
			"validator is jailed for the blocks it misses.\n  Move the key to another host first ('orama maint global validator migrate'), "+
			"or pass --drop-validator to remove it anyway", target.Host)
	}
	return nil
}

// targetHasGlobalLayer asks the node whether it runs the chain's unit. A node
// that is gone cannot be asked; the answer is then unknown and false.
func (s *chainStep) targetHasGlobalLayer(target inspector.Node) (bool, error) {
	if s.opts.Offline {
		return false, nil
	}
	probe, err := s.runner.NodeHasChain(target)
	if err != nil {
		return false, clierr.Unavailable("%v\n  If the machine is gone, pass --offline", err)
	}
	return probe.Chain, nil
}

// planRetire reads the node's record from the chain and decides whether there is
// anything to retire.
func (s *chainStep) planRetire(p *decommission.Plan) ([]string, error) {
	reach, err := s.runner.Open(s.ctx, s.chainCandidates(p))
	if err != nil {
		return nil, clierr.Unavailable("retire %s on the chain: %v", s.opts.ChainNodeID, err)
	}
	s.reach = reach
	node, err := s.readNode(s.ctx, reach, s.opts.ChainNodeID)
	if err != nil {
		return nil, clierr.Failure("%v", err)
	}
	switch {
	case node == nil:
		return nil, clierr.NotFound("the chain has no node %q: check the id with 'orama chain node <id>', or pass --no-chain", s.opts.ChainNodeID)
	case node.Gone():
		s.node = nil
		return []string{fmt.Sprintf("node %s is already %s on the chain: nothing to retire there", node.ID, statusWord(node.Status))}, nil
	case node.ReservedBytes > 0:
		return nil, clierr.Conflict("node %s still holds %d bytes reserved by storage deals, and the chain refuses to retire it. "+
			"Let the deals end or be repaired first", node.ID, node.ReservedBytes)
	}
	note, err := hostBinding(*node, p.Target.Host)
	if err != nil {
		return nil, err
	}
	s.node = node
	return []string{fmt.Sprintf("retire node %s on the chain%s (MsgRetireNode, signed by your RootWallet, through %s): its service keys are revoked "+
		"and its bonds start to unbond", node.ID, note, reach.Node.Host)}, nil
}

// hostBinding checks that the node the chain calls id is this machine, from the
// endpoints it registered: a mistyped id would otherwise retire another of the
// operator's nodes, which cannot be undone. A node that registered no endpoint
// cannot be matched, and the plan says so.
func hostBinding(node chainreach.ChainNode, host string) (note string, err error) {
	listed, known := node.ListsHost(host)
	switch {
	case listed:
		return " (its registered endpoint is " + host + ")", nil
	case known:
		return "", clierr.Conflict("node %s on the chain is registered with the endpoints %v, and none is %s: --chain-node-id is probably another node's. "+
			"Check it with 'orama chain node <id>'", node.ID, node.Endpoints, host)
	default:
		return " (it registered no IPv4 endpoint to compare with " + host + ")", nil
	}
}

// chainCandidates are the nodes whose chain can carry the retirement: the
// survivors first, since the target is about to be erased, then the target if it
// is still there.
func (s *chainStep) chainCandidates(p *decommission.Plan) []inspector.Node {
	var out []inspector.Node
	for _, n := range p.Nodes {
		if n.Host != p.Target.Host {
			out = append(out, n)
		}
	}
	if !s.opts.Offline {
		out = append(out, p.Target)
	}
	return out
}

// Before retires the node on the chain, and so stops the removal with nothing
// changed in the cluster if the chain or the RootWallet refuses.
func (s *chainStep) Before(*decommission.Plan) error {
	if s.node == nil {
		return nil
	}
	client, err := s.newClient(s.ctx, s.reach)
	if err != nil {
		return clierr.Unavailable("%v", err)
	}
	operator, err := client.Operator(s.ctx)
	if err != nil {
		return clierr.Auth("%v\n  Unlock your RootWallet and run the command again. Nothing was removed", err)
	}
	if operator != s.node.Operator {
		return clierr.Conflict("node %s belongs to operator %s but your RootWallet signs as %s; only its operator can retire it. Nothing was removed",
			s.node.ID, s.node.Operator, operator)
	}
	receipt, err := client.RetireNode(s.ctx, s.node.ID)
	if err != nil {
		return clierr.Failure("%v\n  Nothing was removed", err)
	}
	fmt.Printf("  ✓ node %s retired on the chain (block %d)\n", s.node.ID, receipt.Height)
	return nil
}

// statusWord is a node status without its prefix, in lower case.
func statusWord(status string) string {
	return strings.ToLower(strings.TrimPrefix(status, "NODE_STATUS_"))
}
