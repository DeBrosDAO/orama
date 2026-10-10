package setup

import (
	"bytes"
	"context"
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/clusterreg"
	"github.com/DeBrosOfficial/network/pkg/globalbind"
)

// consensusBindingOf is the binding of the node's consensus key to the operator
// that the node signed for this registration, checked here against the exact
// statement the chain verifies (orama-global-bind-v1|chain-id|operator|consensus|
// hex(pubkey)); nil for a node whose consensus key is not bound. x/power counts a
// validator toward the operator of the live node that binds its consensus key, so
// a validator whose node does not bind it lands in the shared unlinked bucket.
func (r *runner) consensusBindingOf(n *nodeRun, ident NodeIdentity) (*clusterreg.NodeBinding, error) {
	if !n.plan.BindConsensus {
		return nil, nil
	}
	b := ident.ConsensusBinding
	if b == nil {
		return nil, fmt.Errorf("node %q returned no binding for its consensus key", n.plan.Name)
	}
	signed := globalbind.Binding{Service: b.Service, KeyType: b.KeyType, Pubkey: b.Pubkey, Signature: b.Signature}
	if err := globalbind.Verify(signed, r.net.Manifest.ChainID, r.oper); err != nil {
		return nil, fmt.Errorf("the consensus key binding that node %q signed for operator %s on chain %s: %w", n.plan.Name, r.oper, r.net.Manifest.ChainID, err)
	}
	return b, nil
}

// bindConsensus puts the consensus binding on a node that is already registered
// without it (a run that stopped before, or a node registered by an older setup).
// A node that holds the binding is left alone, so a re-run sends nothing. The
// update replaces the node's whole binding set, so it carries the bindings the
// node already has.
func (r *runner) bindConsensus(ctx context.Context, sess ChainSession, n *nodeRun, node *RegisteredNode, binding *clusterreg.NodeBinding) error {
	if binding == nil {
		return nil
	}
	if holdsBinding(node.Bindings, *binding) {
		r.emit(n.plan.IP, StepOnchain, StateSkipped, "the consensus key is bound to node "+n.plan.Name)
		return nil
	}
	r.emit(n.plan.IP, StepOnchain, StateRunning, "binding the consensus key to node "+n.plan.Name)
	update := clusterreg.NodeUpdate{Operator: r.oper, NodeID: n.plan.Name, Bindings: replaceBinding(node.Bindings, *binding)}
	if _, err := sess.UpdateNodeBindings(ctx, update); err != nil {
		return fmt.Errorf("bind the consensus key to node %q: %w", n.plan.Name, err)
	}
	return nil
}

// holdsBinding reports whether bindings has want: the same service and key.
func holdsBinding(bindings []clusterreg.NodeBinding, want clusterreg.NodeBinding) bool {
	for _, b := range bindings {
		if b.Service == want.Service && bytes.Equal(b.Pubkey, want.Pubkey) {
			return true
		}
	}
	return false
}

// replaceBinding is bindings with the one for want's service set to want.
func replaceBinding(bindings []clusterreg.NodeBinding, want clusterreg.NodeBinding) []clusterreg.NodeBinding {
	out := make([]clusterreg.NodeBinding, 0, len(bindings)+1)
	for _, b := range bindings {
		if b.Service != want.Service {
			out = append(out, b)
		}
	}
	return append(out, want)
}
