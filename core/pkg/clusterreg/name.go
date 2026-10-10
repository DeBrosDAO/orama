package clusterreg

import "fmt"

const (
	// ClaimNodeNameTypeURL is the Any type URL of orama.nodes.v1.MsgClaimNodeName.
	ClaimNodeNameTypeURL = "/orama.nodes.v1.MsgClaimNodeName"
	// ReleaseNodeNameTypeURL is the Any type URL of orama.nodes.v1.MsgReleaseNodeName.
	ReleaseNodeNameTypeURL = "/orama.nodes.v1.MsgReleaseNodeName"
)

// NodeNameClaim is MsgClaimNodeName: the operator claims Name for its node NodeID. The name's
// grammar and reserved words are the chain's (pkg/nodenames.ValidateName), checked by the caller
// that chooses the name; this package checks the operator and the node id.
type NodeNameClaim struct {
	Operator string
	NodeID   string
	Name     string
}

// ValidateNodeNameClaim checks the operator, the node id and that a name is given.
func ValidateNodeNameClaim(c NodeNameClaim) error {
	if err := validateNameOwner(c.Operator, c.NodeID); err != nil {
		return err
	}
	if c.Name == "" {
		return fmt.Errorf("name must not be empty")
	}
	return nil
}

// EncodeClaimNodeName is the protobuf orama.nodes.v1.MsgClaimNodeName. Field numbers match
// chain/x/nodes/types/tx.pb.go.
func EncodeClaimNodeName(c NodeNameClaim) []byte {
	out := appendStringField(nil, 1, c.Operator)
	out = appendStringField(out, 2, c.NodeID)
	return appendStringField(out, 3, c.Name)
}

// NodeNameRelease is MsgReleaseNodeName: the operator gives up the name of its node NodeID.
type NodeNameRelease struct {
	Operator string
	NodeID   string
}

// ValidateNodeNameRelease checks the operator and the node id.
func ValidateNodeNameRelease(r NodeNameRelease) error {
	return validateNameOwner(r.Operator, r.NodeID)
}

// EncodeReleaseNodeName is the protobuf orama.nodes.v1.MsgReleaseNodeName.
func EncodeReleaseNodeName(r NodeNameRelease) []byte {
	out := appendStringField(nil, 1, r.Operator)
	return appendStringField(out, 2, r.NodeID)
}

func validateNameOwner(operator, nodeID string) error {
	if _, err := CanonicalAccount(operator); err != nil {
		return fmt.Errorf("operator: %w", err)
	}
	if !idPattern.MatchString(nodeID) {
		return fmt.Errorf("node id %q must match %s", nodeID, idPattern.String())
	}
	return nil
}
