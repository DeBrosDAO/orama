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
	validator func(ctx context.Context, env, host string) (bool, error)
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
	if err := s.checkValidator(p.Target); err != nil {
		return nil, err
	}
	hasGlobal, err := s.targetHasGlobalLayer(p.Target)
	if err != nil {
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
		return nil, clierr.Usage("%s is gone, so it cannot be asked whether it was registered on the chain.\n"+
			"  Retire it there too: --chain-node-id <the node's id in x/nodes>\n"+
			"  Or say it has nothing on the chain: --no-chain", p.Target.Host)
	case s.opts.ChainNodeID == "":
		return nil, nil
	}
	return s.planRetire(p)
}

// checkValidator refuses to erase a node that signs for the validator set.
func (s *chainStep) checkValidator(target inspector.Node) error {
	if s.opts.Offline || s.opts.DropValidator {
		return nil
	}
	isValidator, err := s.validator(s.ctx, s.env, target.Host)
	if err != nil {
		return clierr.Unavailable("could not read whether %s is in the validator set: %v\n  If the machine is gone, pass --offline", target.Host, err)
	}
	if isValidator {
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
	s.node = node
	return []string{fmt.Sprintf("retire node %s on the chain (MsgRetireNode, signed by your RootWallet, through %s): its service keys are revoked "+
		"and its bonds start to unbond", node.ID, reach.Node.Host)}, nil
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
