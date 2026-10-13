package lifecycle

import (
	"fmt"
	"sort"
	"strings"
)

// Forgotten reports whether every membership view has dropped wgIP.
//
// The assertion for the kill-a-voter scenario: a dead node is not gone because
// it stopped answering, it is gone when it has left the report's node list
// (by its wireguard_ip) and no surviving node carries it as a WireGuard peer.
// It does not read raft membership. Through the gateway telemetry API, the
// node list is the cluster's node registry as the gateway sees it, so a node
// drops out of it once the registry does. A node that is evicted but left in
// the WireGuard mesh is the shape of failure that survives a restart.
func (r *Report) Forgotten(wgIP string) error {
	var stillThere []string
	for _, n := range r.Nodes {
		if n.Report == nil {
			continue
		}
		if n.Report.WGIP == wgIP {
			stillThere = append(stillThere, n.Host+": still in the node list")
			continue
		}
		if wg := n.Report.WireGuard; wg != nil {
			for _, p := range wg.Peers {
				if allowsIP(p.AllowedIPs, wgIP) {
					stillThere = append(stillThere, n.Host+": still a wireguard peer")
				}
			}
		}
	}
	if len(stillThere) == 0 {
		return nil
	}
	sort.Strings(stillThere)
	return fmt.Errorf("%s has not been forgotten: %s", wgIP, strings.Join(stillThere, "; "))
}

// allowsIP reports whether a peer's allowed-ips list ("10.0.0.3/32", or
// several comma-separated) routes ip.
func allowsIP(allowed, ip string) bool {
	for _, a := range strings.Split(allowed, ",") {
		a = strings.TrimSpace(a)
		if a == ip || strings.HasPrefix(a, ip+"/") {
			return true
		}
	}
	return false
}

// Serving reports whether every node answers on the public surfaces, ignoring
// raft entirely.
//
// The reboot-everything scenario asserts this first: a cluster with no leader
// yet should still be serving DNS and TLS, and the two failures are worth
// telling apart.
func (r *Report) Serving() error {
	var down []string
	for _, n := range r.Nodes {
		if n.Report == nil || n.Report.Gateway == nil || !n.Report.Gateway.Responsive {
			down = append(down, n.Host+": gateway")
		}
		if strings.HasPrefix(n.Role, nameserverRolePrefix) && (n.Report == nil || n.Report.DNS == nil || !n.Report.DNS.CoreDNSActive) {
			down = append(down, n.Host+": coredns")
		}
	}
	if len(down) == 0 {
		return nil
	}
	sort.Strings(down)
	return fmt.Errorf("not serving: %s", strings.Join(down, "; "))
}
