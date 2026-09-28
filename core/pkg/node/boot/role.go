package boot

import (
	"fmt"
	"strings"
)

// Role is which machine a node is. A machine is either a cluster node or a
// global node. both is reserved until the two can share a machine, and it is
// refused rather than treated as one of the others.
type Role string

const (
	RoleCluster Role = "cluster"
	RoleGlobal  Role = "global"
)

// ParseRole parses a role from config or preferences. Empty is cluster, which
// is every node installed before the global role existed.
func ParseRole(raw string) (Role, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", string(RoleCluster):
		return RoleCluster, nil
	case string(RoleGlobal):
		return RoleGlobal, nil
	case "both":
		return "", fmt.Errorf("role \"both\" is refused: a machine is either a cluster node or a global node, not both")
	default:
		return "", fmt.Errorf("unknown node role %q (want cluster or global)", raw)
	}
}
