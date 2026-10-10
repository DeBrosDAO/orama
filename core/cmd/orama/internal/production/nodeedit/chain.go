package nodeedit

import (
	"context"
	"fmt"
	"os"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/production/chainreach"
	"github.com/DeBrosOfficial/network/pkg/clusterreg"
	"github.com/DeBrosOfficial/network/pkg/inspector"
	"github.com/DeBrosOfficial/network/pkg/onchain"
	"github.com/DeBrosOfficial/network/pkg/rwagent"
)

// txClient is the operator's transaction client (onchain.Client).
type txClient interface {
	Operator(ctx context.Context) (string, error)
	DeclareCapacity(ctx context.Context, c clusterreg.Capacity) (*onchain.Receipt, error)
}

// declarer declares a node's storage capacity on the chain.
type declarer struct {
	runner    chainreach.Runner
	readNode  func(ctx context.Context, r *chainreach.Reach, id string) (*chainreach.ChainNode, error)
	newClient func(ctx context.Context, r *chainreach.Reach, want string) (txClient, error)
	// pin is the chain id the environment runs (chainreach.Pin); checkChain refuses a node on another.
	pin        func(env, explicit string) (string, error)
	checkChain func(ctx context.Context, r *chainreach.Reach, want string) error

	reach *chainreach.Reach
	node  *chainreach.ChainNode
	// chainID is the chain the declaration is signed for, decided before anything is changed.
	chainID string
}

func newDeclarer() *declarer {
	return &declarer{
		runner: chainreach.SSH(),
		readNode: func(ctx context.Context, r *chainreach.Reach, id string) (*chainreach.ChainNode, error) {
			return r.ChainNode(ctx, id)
		},
		newClient: func(ctx context.Context, r *chainreach.Reach, want string) (txClient, error) {
			return r.Client(ctx, rwagent.New(os.Getenv("RW_AGENT_SOCK")), want)
		},
		pin: chainreach.Pin,
		checkChain: func(ctx context.Context, r *chainreach.Reach, want string) error {
			return r.CheckChain(ctx, want)
		},
	}
}

func (d *declarer) close() {
	if d.reach != nil {
		_ = d.reach.Close()
	}
}

// preflight reads the node's record from the chain and refuses a declaration
// the chain would refuse, before anything is changed. candidates are the nodes
// whose chain can carry it, the edited node first.
func (d *declarer) preflight(ctx context.Context, env, explicitChainID, nodeID, host string, bytes uint64, candidates []inspector.Node) error {
	chainID, err := d.pin(env, explicitChainID)
	if err != nil {
		return err
	}
	reach, err := d.runner.Open(ctx, candidates)
	if err != nil {
		return clierr.Unavailable("declare the capacity of %s on the chain: %v", nodeID, err)
	}
	d.reach, d.chainID = reach, chainID
	if err := d.checkChain(ctx, reach, chainID); err != nil {
		return clierr.Failure("%v. Nothing was changed", err)
	}
	node, err := d.readNode(ctx, reach, nodeID)
	switch {
	case err != nil:
		return clierr.Failure("%v", err)
	case node == nil:
		return clierr.NotFound("the chain has no node %q: check the id with 'orama chain node <id>'", nodeID)
	case node.Gone():
		return clierr.Conflict("node %s is already %s on the chain, so it cannot declare capacity", nodeID, node.Status)
	case node.ReservedBytes > bytes:
		return clierr.Conflict("deals already reserve %d bytes on node %s; the capacity cannot go below that (asked for %d)", node.ReservedBytes, nodeID, bytes)
	}
	if listed, known := node.ListsHost(host); known && !listed {
		return clierr.Conflict("node %s on the chain is registered with the endpoints %v, and none is %s: --chain-node-id is probably another node's. "+
			"Check it with 'orama chain node <id>'", nodeID, node.Endpoints, host)
	}
	d.node = node
	return nil
}

// exitRoleWarning compares the relay policy asked for (exit or plain) with the exit role the chain
// holds for the node, and says how to bring them together when they differ. "" means they agree.
// The edit changes the relay on the node only; the role and its bond are the chain's, so a
// difference is a warning, never a refusal. A chain that cannot be read is said in the warning.
func (d *declarer) exitRoleWarning(ctx context.Context, nodeID string, exit bool, candidates []inspector.Node) string {
	if d.reach == nil {
		reach, err := d.runner.Open(ctx, candidates)
		if err != nil {
			return fmt.Sprintf("the relay's exit policy was not compared with node %s's exit role on the chain: %v", nodeID, err)
		}
		d.reach = reach
	}
	node, err := d.readNode(ctx, d.reach, nodeID)
	switch {
	case err != nil:
		return fmt.Sprintf("the relay's exit policy was not compared with node %s's exit role on the chain: %v", nodeID, err)
	case node == nil:
		return fmt.Sprintf("the chain has no node %q, so the relay's exit policy cannot be compared with its exit role: check the id with 'orama chain node <id>'", nodeID)
	case exit && !node.HasRole("exit"):
		return fmt.Sprintf("node %s holds no exit role on the chain, so it will carry exit traffic that the chain neither counts nor rewards; "+
			"the role is part of the node's registration and is bonded with 'orama global bond --role exit --id %s'", nodeID, nodeID)
	case !exit && node.HasRole("exit"):
		return fmt.Sprintf("node %s still holds the exit role on the chain, with its bond, while its relay stops being an exit; "+
			"if that is not wanted, release the bond with 'orama global unbond --role exit --id %s'", nodeID, nodeID)
	}
	return ""
}

// declare sends MsgDeclareCapacity, signed by the RootWallet, which must be the
// node's operator.
func (d *declarer) declare(ctx context.Context, bytes uint64) (int64, error) {
	client, err := d.newClient(ctx, d.reach, d.chainID)
	if err != nil {
		return 0, clierr.Unavailable("%v", err)
	}
	operator, err := client.Operator(ctx)
	if err != nil {
		return 0, clierr.Auth("%v\n  Unlock your RootWallet and run the command again. Nothing was changed", err)
	}
	if operator != d.node.Operator {
		return 0, clierr.Conflict("node %s belongs to operator %s but your RootWallet signs as %s; only its operator can declare its capacity. Nothing was changed",
			d.node.ID, d.node.Operator, operator)
	}
	receipt, err := client.DeclareCapacity(ctx, clusterreg.Capacity{NodeID: d.node.ID, Bytes: bytes})
	if err != nil {
		return 0, clierr.Failure("%v\n  Nothing was changed", err)
	}
	return receipt.Height, nil
}

// declaredMessage is what is printed once the chain has the capacity.
func declaredMessage(nodeID string, gb uint64, height int64) string {
	return fmt.Sprintf("  ✓ the chain declares %d GB for node %s (block %d)", gb, nodeID, height)
}
