package onchain

import (
	"context"
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/clusterreg"
)

// RegisterRelay registers the Tor relay of one of the signing operator's nodes (MsgRegisterRelay).
// The node must hold the relay role and bind the relay's ed25519 identity as its "relay" service
// key; r carries the relay's RSA fingerprint and the cross-certificate signed with that identity.
func (c *Client) RegisterRelay(ctx context.Context, r clusterreg.RelayRegistration) (*Receipt, error) {
	r, err := c.relayFor(ctx, r)
	if err != nil {
		return nil, err
	}
	return c.sendMsg(ctx, "register the relay of node "+r.NodeID, clusterreg.RegisterRelayTypeURL, clusterreg.EncodeRegisterRelay(r))
}

// UpdateRelay replaces the fingerprint of the relay registered on one of the signing operator's
// nodes (MsgUpdateRelay), and the ed25519 key the chain holds for it, after a rotation.
func (c *Client) UpdateRelay(ctx context.Context, r clusterreg.RelayRegistration) (*Receipt, error) {
	r, err := c.relayFor(ctx, r)
	if err != nil {
		return nil, err
	}
	return c.sendMsg(ctx, "update the relay of node "+r.NodeID, clusterreg.UpdateRelayTypeURL, clusterreg.EncodeUpdateRelay(r))
}

// relayFor fills the signing operator into r and checks it.
func (c *Client) relayFor(ctx context.Context, r clusterreg.RelayRegistration) (clusterreg.RelayRegistration, error) {
	operator, err := c.operatorFor(ctx, r.Operator)
	if err != nil {
		return r, err
	}
	r.Operator = operator
	if err := clusterreg.ValidateRelay(r); err != nil {
		return r, fmt.Errorf("relay of node %q: %w", r.NodeID, err)
	}
	return r, nil
}
