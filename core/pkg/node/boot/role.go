package boot

import (
	"fmt"
	"strings"
)

// Role is which machine a node is: a cluster node, a global node, or both.
// A machine that is both keeps the cluster node in the root network
// namespace and runs the global services in the orama-global one
// (pkg/globalnetns), so the two share neither loopback nor ports. Both is
// accepted only when that layout is installed.
type Role string

const (
	RoleCluster Role = "cluster"
	RoleGlobal  Role = "global"
	RoleBoth    Role = "both"
)

// RunsClusterGraph reports whether this process converges the cluster
// components. On a co-located machine it does: the global services are their
// own units in the namespace and are not components of this process.
func (r Role) RunsClusterGraph() bool { return r == RoleCluster || r == RoleBoth }

// ParseRole parses a role from config or preferences. Empty is cluster, which
// is every node installed before the global role existed. verifyNetns checks
// that the network-namespace layout is installed; "both" is refused without
// one, and when verifyNetns is nil.
func ParseRole(raw string, verifyNetns func() error) (Role, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", string(RoleCluster):
		return RoleCluster, nil
	case string(RoleGlobal):
		return RoleGlobal, nil
	case string(RoleBoth):
		if verifyNetns == nil {
			return "", fmt.Errorf("role \"both\" needs the network namespace layout, and none was checked")
		}
		if err := verifyNetns(); err != nil {
			return "", fmt.Errorf("role \"both\" is refused without the network namespace layout (orama global install --colocated): %w", err)
		}
		return RoleBoth, nil
	default:
		return "", fmt.Errorf("unknown node role %q (want cluster, global or both)", raw)
	}
}
