package onchain

import (
	"context"
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/clusterreg"
)

// UpdateNodeBindings replaces the binding set of one of the signing operator's
// nodes (MsgUpdateNode). u.Bindings is the whole set the node holds afterwards;
// its Operator is the signing account, and is filled in when empty.
func (c *Client) UpdateNodeBindings(ctx context.Context, u clusterreg.NodeUpdate) (*Receipt, error) {
	operator, err := c.operatorFor(ctx, u.Operator)
	if err != nil {
		return nil, err
	}
	u.Operator = operator
	if err := clusterreg.ValidateNodeUpdate(u); err != nil {
		return nil, fmt.Errorf("update the bindings of node %q: %w", u.NodeID, err)
	}
	return c.sendMsg(ctx, "update the bindings of node "+u.NodeID, clusterreg.UpdateNodeTypeURL, clusterreg.EncodeUpdateNode(u))
}
