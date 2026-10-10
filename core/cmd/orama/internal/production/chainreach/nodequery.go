package chainreach

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/DeBrosOfficial/network/pkg/chainread"
)

const (
	// queryNode is x/nodes' read of one node.
	queryNode = "orama.nodes.v1.Query/Node"
	// StatusRetired and StatusTombstoned are the node states that end its life
	// on the chain.
	StatusRetired    = "NODE_STATUS_RETIRED"
	StatusTombstoned = "NODE_STATUS_TOMBSTONED"
)

// ChainNode is a node's record in x/nodes.
type ChainNode struct {
	ID       string
	Operator string
	Status   string
	// DeclaredBytes and ReservedBytes are the storage capacity the node declared
	// and the part deals have reserved.
	DeclaredBytes, ReservedBytes uint64
}

// Gone reports whether the node has left the chain for good.
func (n ChainNode) Gone() bool { return n.Status == StatusRetired || n.Status == StatusTombstoned }

// ChainNode reads one node's record through the chain's RPC. It returns nil when the
// chain has no such node.
func (r *Reach) ChainNode(ctx context.Context, id string) (*ChainNode, error) {
	req, err := json.Marshal(map[string]string{"node_id": id})
	if err != nil {
		return nil, err
	}
	raw, err := (&chainread.Reader{RPC: r.RPCBase}).Query(ctx, queryNode, string(req))
	if errors.Is(err, chainread.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read node %q from the chain of %s: %w", id, r.Node.Host, err)
	}
	return parseChainNode(raw)
}

// parseChainNode reads the JSON of QueryNodeResponse (proto field names, 64-bit
// integers as strings).
func parseChainNode(raw json.RawMessage) (*ChainNode, error) {
	var resp struct {
		Node struct {
			NodeID        string `json:"node_id"`
			Operator      string `json:"operator"`
			Status        string `json:"status"`
			DeclaredBytes string `json:"declared_capacity_bytes"`
			ReservedBytes string `json:"reserved_capacity_bytes"`
		} `json:"node"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("the chain answered a malformed node: %w", err)
	}
	declared, err := parseBytes(resp.Node.DeclaredBytes)
	if err != nil {
		return nil, fmt.Errorf("declared_capacity_bytes: %w", err)
	}
	reserved, err := parseBytes(resp.Node.ReservedBytes)
	if err != nil {
		return nil, fmt.Errorf("reserved_capacity_bytes: %w", err)
	}
	return &ChainNode{
		ID: resp.Node.NodeID, Operator: resp.Node.Operator, Status: resp.Node.Status,
		DeclaredBytes: declared, ReservedBytes: reserved,
	}, nil
}

// parseBytes reads a protojson uint64: a decimal string, "0" or absent when zero.
func parseBytes(s string) (uint64, error) {
	if s == "" {
		return 0, nil
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%q is not a byte count", s)
	}
	return n, nil
}
