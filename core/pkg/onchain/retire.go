package onchain

import (
	"context"
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/clusterreg"
)

// RetireNode retires one of the signing operator's nodes (MsgRetireNode). The
// chain revokes the node's service bindings and queues its role bonds to unbond;
// it refuses while deals still reserve bytes on the node.
func (c *Client) RetireNode(ctx context.Context, nodeID string) (*Receipt, error) {
	operator, err := c.Operator(ctx)
	if err != nil {
		return nil, err
	}
	r := clusterreg.Retire{Operator: operator, ID: nodeID}
	if err := clusterreg.ValidateRetire(r); err != nil {
		return nil, fmt.Errorf("retire node %q: %w", nodeID, err)
	}
	return c.sendMsg(ctx, "retire node "+nodeID, clusterreg.RetireNodeTypeURL, clusterreg.EncodeRetire(r))
}
