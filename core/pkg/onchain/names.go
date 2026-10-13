package onchain

import (
	"context"
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/clusterreg"
)

// ClaimNodeName claims name for one of the signing operator's nodes (MsgClaimNodeName). The chain
// locks the name deposit, allows a node one name and a name one node, and refuses a reserved or
// malformed name; callers check the name's grammar first so the operator is told before a fee is
// paid.
func (c *Client) ClaimNodeName(ctx context.Context, nodeID, name string) (*Receipt, error) {
	operator, err := c.Operator(ctx)
	if err != nil {
		return nil, err
	}
	claim := clusterreg.NodeNameClaim{Operator: operator, NodeID: nodeID, Name: name}
	if err := clusterreg.ValidateNodeNameClaim(claim); err != nil {
		return nil, fmt.Errorf("claim the name %q for node %q: %w", name, nodeID, err)
	}
	return c.sendMsg(ctx, fmt.Sprintf("claim the name %q for node %s", name, nodeID), clusterreg.ClaimNodeNameTypeURL, clusterreg.EncodeClaimNodeName(claim))
}

// ReleaseNodeName gives up the name of one of the signing operator's nodes (MsgReleaseNodeName)
// and gets its deposit back.
func (c *Client) ReleaseNodeName(ctx context.Context, nodeID string) (*Receipt, error) {
	operator, err := c.Operator(ctx)
	if err != nil {
		return nil, err
	}
	release := clusterreg.NodeNameRelease{Operator: operator, NodeID: nodeID}
	if err := clusterreg.ValidateNodeNameRelease(release); err != nil {
		return nil, fmt.Errorf("release the name of node %q: %w", nodeID, err)
	}
	return c.sendMsg(ctx, "release the name of node "+nodeID, clusterreg.ReleaseNodeNameTypeURL, clusterreg.EncodeReleaseNodeName(release))
}
