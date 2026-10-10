// Package removenode is `orama remove`: the guided, safe removal of one node.
// It is the cluster-side removal (production/decommission: the quorum check, the
// tombstone, the retirement, the wipe) with the node's chain registration retired
// first, and with the refusals a newcomer needs: a node that signs for the
// validator set is not erased by accident, and a node that may be registered on
// the chain is not erased without saying what happens to its bond.
package removenode

import (
	"context"

	"github.com/DeBrosOfficial/network/cmd/orama/internal"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/production/decommission"
)

// Options are the flags of `orama remove`.
type Options struct {
	// Env is the network the node belongs to; empty is the active one.
	Env string
	// Node is the public IP of the node to remove.
	Node string
	// Offline: the machine is already gone; retire it cluster-side only.
	Offline bool
	// Nuclear also removes the shared binaries and the Tor package when wiping.
	Nuclear bool
	// Yes skips the confirmation. DryRun prints the plan and changes nothing.
	Yes, DryRun bool
	// ChainNodeID is the node's id in x/nodes, to retire it on the chain.
	ChainNodeID string
	// NoChain leaves the node's chain registration alone.
	NoChain bool
	// DropValidator accepts that a node in the validator set stops signing and
	// its consensus key is erased with it.
	DropValidator bool
}

// Run is `orama remove`.
func Run(ctx context.Context, opts Options) error {
	if err := opts.validate(); err != nil {
		return err
	}
	env := opts.Env
	if env == "" {
		active, err := cli.GetActiveEnvironment()
		if err != nil {
			return clierr.Usage("no --env given and no active network: %v", err)
		}
		env = active.Name
	}
	step := newChainStep(ctx, env, opts)
	defer step.close()
	return decommission.Run(&decommission.Flags{
		Env: env, Node: opts.Node, Offline: opts.Offline, Nuclear: opts.Nuclear,
		Force: opts.Yes, DryRun: opts.DryRun, Extension: step,
	})
}

// validate checks the flags against each other before any node is reached.
func (o Options) validate() error {
	switch {
	case o.Node == "":
		return clierr.Usage("--node is required: remove takes out one node; give its public IP")
	case o.NoChain && o.ChainNodeID != "":
		return clierr.Usage("--no-chain and --chain-node-id contradict each other: either the node's chain registration is retired or it is left")
	}
	return nil
}
