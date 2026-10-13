//go:build e2e_fleet

package tenancy

import (
	"context"
	"errors"
	"net"
	"sort"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// dnsBudget bounds one query to one nameserver.
const dnsBudget = 10 * time.Second

// Nameservers are the run's nodes that answer DNS (every core node when none
// is marked nameserver).
func Nameservers(f *fleet.Fleet) []fleet.Node {
	var out []fleet.Node
	for _, n := range f.State.Nodes {
		if n.Role == fleet.RoleNameserver {
			out = append(out, n)
		}
	}
	if len(out) == 0 {
		return f.State.Nodes
	}
	return out
}

// ResolveAt asks the nameserver at ip:53 for host's A records, bypassing
// every cache between the runner and the fleet. NXDOMAIN is an empty answer
// with no error.
func ResolveAt(ctx context.Context, ip, host string) ([]string, error) {
	r := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: dnsBudget}).DialContext(ctx, network, net.JoinHostPort(ip, "53"))
	}}
	ctx, cancel := context.WithTimeout(ctx, dnsBudget)
	defer cancel()
	addrs, err := r.LookupHost(ctx, host)
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
		return nil, nil
	}
	sort.Strings(addrs)
	return addrs, err
}

// NamespaceHost is the namespace gateway's DNS name.
func NamespaceHost(f *fleet.Fleet, name string) string {
	return "ns-" + name + "." + f.State.BaseDomain
}
