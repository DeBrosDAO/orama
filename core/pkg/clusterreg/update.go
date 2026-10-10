package clusterreg

import "fmt"

// UpdateNodeTypeURL is the Any type URL of orama.nodes.v1.MsgUpdateNode.
const UpdateNodeTypeURL = "/orama.nodes.v1.MsgUpdateNode"

// ConsensusService is the service name of the binding that holds a validator's
// ed25519 consensus key. It matches chain/x/nodes/types.ConsensusService.
const ConsensusService = "consensus"

// NodeUpdate is MsgUpdateNode as far as setup uses it: it replaces a node's whole
// binding set and changes nothing else (the hot key, endpoints, region and ASN
// are left as they are).
type NodeUpdate struct {
	Operator string
	NodeID   string
	// Bindings is the complete set the node holds afterwards: the chain replaces
	// the stored set with it, so it carries the bindings the node already had.
	Bindings []NodeBinding
}

// ValidateNodeUpdate checks the stateless rules x/nodes ValidateBasic checks for
// an update of the bindings. The chain checks the signatures and the hot-key
// binding against the stored hot key.
func ValidateNodeUpdate(u NodeUpdate) error {
	if _, err := CanonicalAccount(u.Operator); err != nil {
		return fmt.Errorf("operator: %w", err)
	}
	if !idPattern.MatchString(u.NodeID) {
		return fmt.Errorf("node id %q must match %s", u.NodeID, idPattern.String())
	}
	if len(u.Bindings) == 0 {
		return fmt.Errorf("an update of the bindings carries at least one binding")
	}
	return validateNodeBindings(u.Bindings)
}

// EncodeUpdateNode is the protobuf orama.nodes.v1.MsgUpdateNode with the
// operator, the node id and the bindings (fields 1, 2 and 4).
func EncodeUpdateNode(u NodeUpdate) []byte {
	b := appendStringField(nil, 1, u.Operator)
	b = appendStringField(b, 2, u.NodeID)
	for _, binding := range u.Bindings {
		b = appendBytesField(b, 4, encodeBinding(binding))
	}
	return b
}
