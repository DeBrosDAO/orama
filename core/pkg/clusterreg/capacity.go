package clusterreg

import "fmt"

const (
	// DeclareCapacityTypeURL is the Any type URL of orama.nodes.v1.MsgDeclareCapacity.
	DeclareCapacityTypeURL = "/orama.nodes.v1.MsgDeclareCapacity"
	// RetireNodeTypeURL is the Any type URL of orama.nodes.v1.MsgRetireNode.
	RetireNodeTypeURL = "/orama.nodes.v1.MsgRetireNode"
	// RetireClusterTypeURL is the Any type URL of orama.nodes.v1.MsgRetireCluster.
	RetireClusterTypeURL = "/orama.nodes.v1.MsgRetireCluster"
)

// Capacity is MsgDeclareCapacity. A zero byte count is a real declaration:
// the chain omits the field, and so does EncodeCapacity.
type Capacity struct {
	Operator string
	NodeID   string
	Bytes    uint64
}

// ValidateCapacity checks the operator and the node id. Zero bytes is allowed.
func ValidateCapacity(c Capacity) error {
	if _, err := CanonicalAccount(c.Operator); err != nil {
		return fmt.Errorf("operator: %w", err)
	}
	if !idPattern.MatchString(c.NodeID) {
		return fmt.Errorf("node id %q must match %s", c.NodeID, idPattern.String())
	}
	return nil
}

// EncodeCapacity is the protobuf orama.nodes.v1.MsgDeclareCapacity.
func EncodeCapacity(c Capacity) []byte {
	out := appendStringField(nil, 1, c.Operator)
	out = appendStringField(out, 2, c.NodeID)
	if c.Bytes != 0 {
		out = appendUvarintField(out, 3, c.Bytes)
	}
	return out
}

// Retire is the body of MsgRetireNode and MsgRetireCluster.
type Retire struct {
	Operator string
	ID       string
}

// ValidateRetire checks the operator and the id.
func ValidateRetire(r Retire) error {
	if _, err := CanonicalAccount(r.Operator); err != nil {
		return fmt.Errorf("operator: %w", err)
	}
	if !idPattern.MatchString(r.ID) {
		return fmt.Errorf("id %q must match %s", r.ID, idPattern.String())
	}
	return nil
}

// EncodeRetire is the protobuf body shared by MsgRetireNode and MsgRetireCluster.
func EncodeRetire(r Retire) []byte {
	out := appendStringField(nil, 1, r.Operator)
	return appendStringField(out, 2, r.ID)
}
