package clusterreg

import (
	"encoding/hex"
	"fmt"
	"regexp"
)

var nodeService = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

// RegisterNodeTypeURL is the Any type URL of orama.nodes.v1.MsgRegisterNode.
const RegisterNodeTypeURL = "/orama.nodes.v1.MsgRegisterNode"

// Node roles are the x/nodes Role enum, without the unspecified zero.
const (
	RoleValidator = 1
	RoleStorage   = 2
	RoleRelay     = 3
	RoleExit      = 4
	RoleDirauth   = 5
	RoleArchiver  = 6
)

// NodeBinding is one service key inside MsgRegisterNode.
type NodeBinding struct {
	Service   string
	KeyType   string
	Pubkey    []byte
	Signature []byte
}

// NodeRegistration is MsgRegisterNode's body. Endpoints are public only.
type NodeRegistration struct {
	Operator   string
	NodeID     string
	Roles      []int
	HotKey     string
	Bindings   []NodeBinding
	Endpoints  []string
	RegionHint string
}

// ValidateNode checks the stateless rules x/nodes ValidateBasic checks.
// It does not check binding signatures; the chain does that with the chain id.
func ValidateNode(n NodeRegistration) error {
	operator, err := CanonicalAccount(n.Operator)
	if err != nil {
		return fmt.Errorf("operator: %w", err)
	}
	if !idPattern.MatchString(n.NodeID) {
		return fmt.Errorf("node id %q must match %s", n.NodeID, idPattern.String())
	}
	hot, err := CanonicalAccount(n.HotKey)
	if err != nil {
		return fmt.Errorf("hot key: %w", err)
	}
	if hot == operator {
		return fmt.Errorf("hot key must not be the operator")
	}
	if len(n.Roles) == 0 {
		return fmt.Errorf("at least one role is required")
	}
	seenRole := map[int]struct{}{}
	for _, role := range n.Roles {
		if role < RoleValidator || role > RoleArchiver {
			return fmt.Errorf("unknown role %d", role)
		}
		if _, ok := seenRole[role]; ok {
			return fmt.Errorf("duplicate role %d", role)
		}
		seenRole[role] = struct{}{}
	}
	if len(n.Bindings) == 0 {
		return fmt.Errorf("at least one binding is required")
	}
	if err := validateNodeBindings(n.Bindings); err != nil {
		return err
	}
	if err := validateEndpointsMin(n.Endpoints, 0); err != nil {
		return err
	}
	return validateRegion(n.RegionHint)
}

func validateNodeBindings(bindings []NodeBinding) error {
	if len(bindings) > 64 {
		return fmt.Errorf("got %d bindings, max is 64", len(bindings))
	}
	services := map[string]struct{}{}
	pubs := map[string]struct{}{}
	for i, b := range bindings {
		if !servicePatternNode(b.Service) {
			return fmt.Errorf("binding %d: service %q is not a binding name", i, b.Service)
		}
		var want int
		switch b.KeyType {
		case "secp256k1":
			want = 33
		case "ed25519":
			want = 32
		default:
			return fmt.Errorf("binding %d: unknown key type %s", i, b.KeyType)
		}
		if len(b.Pubkey) != want {
			return fmt.Errorf("binding %d: pubkey is %d bytes, want %d", i, len(b.Pubkey), want)
		}
		if len(b.Signature) != 64 {
			return fmt.Errorf("binding %d: signature is %d bytes, want 64", i, len(b.Signature))
		}
		if _, ok := services[b.Service]; ok {
			return fmt.Errorf("duplicate binding service %q", b.Service)
		}
		services[b.Service] = struct{}{}
		key := hex.EncodeToString(b.Pubkey)
		if _, ok := pubs[key]; ok {
			return fmt.Errorf("duplicate binding pubkey")
		}
		pubs[key] = struct{}{}
	}
	return nil
}

func servicePatternNode(service string) bool {
	return nodeService.MatchString(service)
}

func validateRegion(region string) error {
	if region == "" {
		return nil
	}
	if !idPattern.MatchString(region) {
		return fmt.Errorf("region hint %q must match %s", region, idPattern.String())
	}
	return nil
}

// EncodeRegisterNode is the protobuf orama.nodes.v1.MsgRegisterNode.
func EncodeRegisterNode(n NodeRegistration) []byte {
	b := appendStringField(nil, 1, n.Operator)
	b = appendStringField(b, 2, n.NodeID)
	var roles []uint64
	for _, role := range n.Roles {
		roles = append(roles, uint64(role))
	}
	b = appendPacked(b, 3, roles)
	b = appendStringField(b, 4, n.HotKey)
	for _, binding := range n.Bindings {
		b = appendBytesField(b, 5, encodeBinding(binding))
	}
	for _, ep := range n.Endpoints {
		b = appendStringField(b, 6, ep)
	}
	if n.RegionHint != "" {
		b = appendStringField(b, 7, n.RegionHint)
	}
	return b
}

func encodeBinding(b NodeBinding) []byte {
	out := appendStringField(nil, 1, b.Service)
	keyType := uint64(1)
	if b.KeyType == "ed25519" {
		keyType = 2
	}
	out = appendUvarintField(out, 2, keyType)
	out = appendBytesField(out, 3, b.Pubkey)
	return appendBytesField(out, 4, b.Signature)
}

func appendPacked(dst []byte, field int, vals []uint64) []byte {
	if len(vals) == 0 {
		return dst
	}
	var body []byte
	for _, v := range vals {
		body = appendVarint(body, v)
	}
	return appendBytesField(dst, field, body)
}
