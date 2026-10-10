package repair

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/DeBrosOfficial/network/chain/client/node"
	nodestypes "github.com/DeBrosOfficial/network/chain/x/nodes/types"
	"github.com/DeBrosOfficial/network/chain/x/storage/types"
)

// NodeChain is Chain over one oramad RPC.
type NodeChain struct{ *node.Client }

// Deal reads one deal.
func (c NodeChain) Deal(ctx context.Context, id uint64) (types.Deal, error) {
	var resp types.QueryDealResponse
	if err := c.Query(ctx, "/orama.storage.v1.Query/Deal", &types.QueryDealRequest{DealId: id}, &resp); err != nil {
		return types.Deal{}, err
	}
	return resp.Deal, nil
}

// Slot reads one slot.
func (c NodeChain) Slot(ctx context.Context, dealID uint64, slot uint32) (types.Slot, error) {
	var resp types.QuerySlotResponse
	if err := c.Query(ctx, "/orama.storage.v1.Query/Slot", &types.QuerySlotRequest{DealId: dealID, Slot: slot}, &resp); err != nil {
		return types.Slot{}, err
	}
	return resp.Slot, nil
}

// Height is the latest block height of the oramad this client reads.
func (c NodeChain) Height(ctx context.Context) (int64, error) { return c.LatestHeight(ctx) }

// ProviderURL is the node's first http(s) endpoint in x/nodes.
func (c NodeChain) ProviderURL(ctx context.Context, nodeID string) (string, error) {
	var resp nodestypes.QueryNodeResponse
	if err := c.Query(ctx, "/orama.nodes.v1.Query/Node", &nodestypes.QueryNodeRequest{NodeId: nodeID}, &resp); err != nil {
		return "", err
	}
	return FirstHTTPEndpoint(nodeID, resp.Node.Endpoints)
}

// FirstHTTPEndpoint picks the provider HTTP root from a node's endpoints. The root is rebuilt from
// the endpoint's scheme, host and path alone: the endpoint was written by the node, so a query,
// fragment or userinfo it carries is not passed on to the provider request.
func FirstHTTPEndpoint(nodeID string, endpoints []string) (string, error) {
	for _, ep := range endpoints {
		if !strings.HasPrefix(ep, "http://") && !strings.HasPrefix(ep, "https://") {
			continue
		}
		u, err := url.Parse(ep)
		if err != nil || u.Host == "" {
			continue
		}
		root := url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path, RawPath: u.RawPath}
		return strings.TrimRight(root.String(), "/"), nil
	}
	return "", fmt.Errorf("node %s names no http(s) provider endpoint in x/nodes", nodeID)
}
