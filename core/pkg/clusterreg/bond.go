package clusterreg

import "fmt"

const (
	// BondNodeTypeURL is the Any type URL of orama.nodes.v1.MsgBondNode.
	BondNodeTypeURL = "/orama.nodes.v1.MsgBondNode"
	// UnbondNodeTypeURL is the Any type URL of orama.nodes.v1.MsgUnbondNode.
	UnbondNodeTypeURL = "/orama.nodes.v1.MsgUnbondNode"
)

// Bond is the body of MsgBondNode and MsgUnbondNode. Amount is a positive
// integer of norama, with no leading zero, which is how the chain's math.Int
// encoding writes it.
type Bond struct {
	Operator string
	NodeID   string
	Role     int
	Amount   string
}

// ValidateBond checks the stateless rules of both messages.
func ValidateBond(b Bond) error {
	if _, err := CanonicalAccount(b.Operator); err != nil {
		return fmt.Errorf("operator: %w", err)
	}
	if !idPattern.MatchString(b.NodeID) {
		return fmt.Errorf("node id %q must match %s", b.NodeID, idPattern.String())
	}
	if b.Role < RoleValidator || b.Role > RoleArchiver {
		return fmt.Errorf("unknown role %d", b.Role)
	}
	if !positiveInteger(b.Amount) {
		return fmt.Errorf("amount must be a positive integer of norama")
	}
	return nil
}

// EncodeBond is the protobuf body shared by MsgBondNode and MsgUnbondNode.
// The amount is the decimal text cosmos-sdk math.Int marshals.
func EncodeBond(b Bond) []byte {
	out := appendStringField(nil, 1, b.Operator)
	out = appendStringField(out, 2, b.NodeID)
	out = appendUvarintField(out, 3, uint64(b.Role))
	return appendStringField(out, 4, b.Amount)
}
