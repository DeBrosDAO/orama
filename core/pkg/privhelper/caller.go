package privhelper

import (
	"fmt"
	"strings"
)

// Who may ask for what.
//
// The helper used to serve any process running as the orama user: the uid was
// the whole check. Every Orama daemon runs as that user — each tenant's
// gateway, which runs tenant-supplied WASM, and the internet-facing Caddy among
// them — so any of them could rewrite the node's WireGuard peers, write the env
// file of any namespace's units or start and stop any namespace's services.
//
// A request is now authorised by the systemd unit the calling process runs in,
// read from its cgroup, which the kernel assigns and a process cannot change
// for itself:
//
//   - root: everything. The installer and the CLI run as root.
//   - orama-node.service, the node supervisor: everything the allow-list
//     permits. It owns the mesh, the host firewall rules and the @index
//     services.
//   - orama-namespace-gateway@index.service, the cluster gateway: the tools
//     its namespace cluster manager, deployment runner and join handlers use —
//     starting and stopping namespace, deployment and host TURN units, staging
//     unit and deployment environments, opening TURN ports, and adding or
//     removing the one WireGuard peer a join, an enrolment or a node removal is
//     about (it must be reachable before the join answers, not a sync later).
//     Not the whole-mesh rewrite (persist-peers), which is orama-node's sync,
//     and not the legacy host units or wg-quick, which only the node's
//     migrations touch. The peer grant gives the cluster gateway nothing it
//     does not have already: it writes wireguard_peers in the registry, and
//     every node's sync applies those rows.
//   - anything else, a tenant's gateway included: nothing. A tenant gateway
//     manages no units — namespace services are started by the cluster gateway
//     and deployments run from the cluster registry, which a tenant gateway
//     does not hold — and its deployment instances could not be told apart
//     from another namespace's by name (pkg/deployments/process InstanceName
//     is not injective), so no narrower grant would be sound.

// Units the helper serves.
const (
	// NodeUnit is the node supervisor.
	NodeUnit = "orama-node.service"
	// IndexGatewayUnit is the cluster gateway.
	IndexGatewayUnit = "orama-namespace-gateway@index.service"
)

// Caller is who sent a request: the uid the kernel reports for the peer and,
// for a caller that is not root, the systemd unit its process runs in.
type Caller struct {
	UID  uint32
	Unit string
}

// Authorize reports whether c may make the (already validated) request inv.
func Authorize(c Caller, inv Invocation) error {
	if c.UID == 0 {
		return nil
	}
	switch c.Unit {
	case NodeUnit:
		return nil
	case IndexGatewayUnit:
		return authorizeIndexGateway(inv)
	default:
		return fmt.Errorf("a process in %q may not use the privileged helper; only %s and %s may",
			c.Unit, NodeUnit, IndexGatewayUnit)
	}
}

// authorizeIndexGateway allows the cluster gateway what its cluster manager,
// deployment runner and join handlers do.
func authorizeIndexGateway(inv Invocation) error {
	switch inv.Tool {
	case ToolWireGuard:
		if len(inv.Args) > 0 && (inv.Args[0] == wgAddPeer || inv.Args[0] == wgRemovePeer) {
			return nil
		}
		return fmt.Errorf("rewriting the WireGuard mesh is orama-node's; %s may add or remove one peer only", IndexGatewayUnit)
	case ToolSystemctl:
		return authorizeGatewaySystemctl(inv.Args)
	case ToolGatewayKey:
		return nil // put of the index gateway's own signing key; Validate allows no other
	default:
		return nil
	}
}

// authorizeGatewaySystemctl allows the unit families the cluster gateway
// manages: namespace services, deployments and the host TURN server.
func authorizeGatewaySystemctl(args []string) error {
	if len(args) == 1 && args[0] == "daemon-reload" {
		return nil
	}
	if len(args) > 0 && args[0] == "set-property" {
		return nil // Validate allows it on deployment units only
	}
	if len(args) == 2 {
		unit := args[1]
		if namespaceUnit.MatchString(unit) || deployUnit.MatchString(unit) || unit == hostTURNUnit {
			return nil
		}
	}
	return fmt.Errorf("%s may not run systemctl %q; the legacy host units and wg-quick are orama-node's",
		IndexGatewayUnit, strings.Join(args, " "))
}

// UnitFromCgroup is the systemd unit a process runs in, from the content of
// its /proc/<pid>/cgroup: the first ".service" component of a cgroup v2 path
// that starts at /system.slice/ ("0::/system.slice/orama-node.service").
//
// A user session can name its own service orama-node.service. That path lives
// under /user.slice, and accepting it would treat the user's process as
// orama-node. ".." is rejected for the same reason: a path that climbs out of
// system.slice is not a system unit. A process in no service — a login
// session, a scope, a cgroup v1-only host — has none, and is an error.
func UnitFromCgroup(content string) (string, error) {
	for _, line := range strings.Split(content, "\n") {
		path, ok := strings.CutPrefix(strings.TrimSpace(line), "0::")
		if !ok {
			continue
		}
		if !strings.HasPrefix(path, "/system.slice/") {
			return "", fmt.Errorf("cgroup %q is not under /system.slice", path)
		}
		parts := strings.Split(path, "/")
		for i, part := range parts {
			if part == "" {
				if i == 0 {
					continue // the path is absolute
				}
				return "", fmt.Errorf("cgroup %q is not a system.slice path", path)
			}
			if part == "." || part == ".." {
				return "", fmt.Errorf("cgroup %q is not a system.slice path", path)
			}
			if strings.HasSuffix(part, ".service") {
				return part, nil
			}
		}
		return "", fmt.Errorf("cgroup %q is not inside a systemd service", path)
	}
	return "", fmt.Errorf("no cgroup v2 entry in %q; the helper needs the unified hierarchy to tell callers apart", strings.TrimSpace(content))
}
