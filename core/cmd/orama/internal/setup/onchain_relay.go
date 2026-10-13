package setup

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/clusterreg"
	"github.com/DeBrosOfficial/network/pkg/globalbind"
)

// relayIdentityOf is the identity of the node's Tor relay that the node signed for this registration,
// checked here against exactly what the chain verifies: the binding of the relay's ed25519 key as the
// operator's service key "relay", and the cross-certificate of its RSA fingerprint for this node and its
// exit declaration, which the RSA proof covers too. nil
// for a node that carries no relay. A relay whose identity does not verify would be refused by the
// chain after the registration and the bond were paid for, so it stops the run before any transaction.
func (r *runner) relayIdentityOf(n *nodeRun, ident NodeIdentity) (*globalbind.RelayIdentity, error) {
	if !slices.Contains(n.plan.Roles, clusterreg.RoleRelay) {
		return nil, nil
	}
	if len(ident.RelayIdentity) == 0 {
		return nil, fmt.Errorf("node %q returned no identity for its Tor relay: is the relay installed and started? `orama global tor info` on the node shows its fingerprint once it has run", n.plan.Name)
	}
	id, err := globalbind.ParseRelayIdentity(ident.RelayIdentity, r.net.Manifest.ChainID, r.oper, n.plan.Name, slices.Contains(n.plan.Roles, clusterreg.RoleExit))
	if err != nil {
		return nil, fmt.Errorf("the Tor relay identity that node %q signed for operator %s on chain %s: %w", n.plan.Name, r.oper, r.net.Manifest.ChainID, err)
	}
	return &id, nil
}

// relayBinding is the relay's key as the node binding x/relay reads it from.
func relayBinding(id *globalbind.RelayIdentity) clusterreg.NodeBinding {
	return clusterreg.NodeBinding{Service: id.Binding.Service, KeyType: id.Binding.KeyType, Pubkey: id.Binding.Pubkey, Signature: id.Binding.Signature}
}

// bindRelayKey puts the relay's key on a node that is registered without it, or with another one (a
// run that stopped before, a node registered by an older setup, a relay whose Tor identity was
// replaced). A node that holds the binding is left alone, so a re-run sends nothing. The update
// replaces the node's whole binding set, so it carries the bindings the node already has.
func (r *runner) bindRelayKey(ctx context.Context, sess ChainSession, n *nodeRun, node *RegisteredNode, relay *globalbind.RelayIdentity) error {
	if relay == nil {
		return nil
	}
	binding := relayBinding(relay)
	if holdsBinding(node.Bindings, binding) {
		r.emit(n.plan.IP, StepOnchain, StateSkipped, "the relay's key is bound to node "+n.plan.Name)
		return nil
	}
	r.emit(n.plan.IP, StepOnchain, StateRunning, "binding the relay's key to node "+n.plan.Name)
	update := clusterreg.NodeUpdate{Operator: r.oper, NodeID: n.plan.Name, Bindings: replaceBinding(node.Bindings, binding)}
	if _, err := sess.UpdateNodeBindings(ctx, update); err != nil {
		return fmt.Errorf("bind the relay's key to node %q: %w", n.plan.Name, err)
	}
	node.Bindings = update.Bindings
	return nil
}

// registerRelay registers the node's Tor relay with x/relay, which pays a relay only once it is
// registered and a directory authority's vote names it by the registered fingerprint and key. A relay
// the chain already holds with this fingerprint, key and exit declaration is left alone; one it holds
// with another fingerprint or key (a rotation) is updated.
func (r *runner) registerRelay(ctx context.Context, sess ChainSession, n *nodeRun, relay *globalbind.RelayIdentity) error {
	if relay == nil {
		return nil
	}
	ip, id := n.plan.IP, n.plan.Name
	exit := slices.Contains(n.plan.Roles, clusterreg.RoleExit)
	reg := clusterreg.RelayRegistration{
		Operator: r.oper, NodeID: id, RSAFingerprint: relay.Fingerprint, Exit: exit, Ed25519Signature: relay.CrossCert,
		RSAPublicKey: relay.RSAPublicKey, RSASignature: relay.RSASignature,
	}
	fingerprint := strings.ToUpper(fmt.Sprintf("%x", relay.Fingerprint))
	held, err := sess.RelayOfNode(ctx, r.oper, id)
	if err != nil {
		return fmt.Errorf("check whether node %q has a relay registered: %w", id, err)
	}
	switch {
	case held == nil:
		r.emit(ip, StepOnchain, StateRunning, "registering the relay "+fingerprint+" of node "+id)
		if _, err := sess.RegisterRelay(ctx, reg); err != nil {
			return fmt.Errorf("register the relay %s of node %q: %w", fingerprint, id, err)
		}
	case bytes.Equal(held.Fingerprint, relay.Fingerprint) && bytes.Equal(held.Ed25519ID, relay.Binding.Pubkey) && held.Exit == exit:
		r.emit(ip, StepOnchain, StateSkipped, "the relay "+fingerprint+" is registered on node "+id)
		return nil
	default:
		r.emit(ip, StepOnchain, StateRunning, "updating the registered relay of node "+id+" to "+fingerprint)
		if _, err := sess.UpdateRelay(ctx, reg); err != nil {
			return fmt.Errorf("update the relay of node %q to %s: %w", id, fingerprint, err)
		}
	}
	r.emit(ip, StepOnchain, StateDone, "relay "+fingerprint+" of node "+id)
	return nil
}
