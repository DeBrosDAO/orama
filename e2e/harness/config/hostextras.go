package config

import (
	"net/netip"
	"strings"
)

// HostListener is a listening socket on a stagenet node that Orama did not
// install: the operator's own access tool or something the machine image
// ships. The e2e audits of the public edge (only edge ports listen on public
// addresses) excuse exactly what is declared here, per node, and say so in the
// test log; they fail on a declaration the node no longer matches, so this list
// cannot rot into a blanket exemption. Only the stagenet target has one: a
// fleet run's servers are fresh images and nothing is excused there.
type HostListener struct {
	// Node is the node name (node-1...); empty declares it for every node.
	Node string
	// Process is the socket owner as ss prints it.
	Process string
	// Proto is tcp or udp, matched as a prefix (tcp6 is tcp).
	Proto string
	// Port is the listening port; 0 covers any port, for a process whose ports
	// are ephemeral.
	Port int
	// Net is the CIDR the socket's bound address must lie in; empty covers any
	// address. A declaration that names a network cannot excuse a wildcard or
	// public-address bind.
	Net string
	// Why is who put it there.
	Why string
}

// HostUFWRule is an untagged allow rule on a stagenet node that the operator
// added: `ufw status` prints To for it. The install firewall audit excuses it
// the same way the listener audit excuses a HostListener.
type HostUFWRule struct {
	Node string
	To   string
	Why  string
}

const (
	whyTailscale = "tailscale is the operator's own access path to the stagenet nodes"
	// tailnetNet is Tailscale's CGNAT range, where tailscaled's TCP sockets bind
	// the node's tailnet address.
	tailnetNet = "100.64.0.0/10"
	whyRPCBind = "rpcbind is socket-activated by the machine image (an NFS client package); Orama neither installs nor starts it, and ufw's default deny keeps 111 off the internet"
)

// StagenetHostListeners are the listeners on the stagenet nodes that are not
// Orama's. Tailscale is on node-1 and node-3 only; its sockets are the
// WireGuard UDP port on every address and ephemeral TCP ports bound only to
// the tailnet address (100.64.0.0/10): a tailscaled TCP socket on any other
// address is not excused.
var StagenetHostListeners = []HostListener{
	{Node: "node-1", Process: "tailscaled", Proto: "udp", Why: whyTailscale},
	{Node: "node-1", Process: "tailscaled", Proto: "tcp", Net: tailnetNet, Why: whyTailscale},
	{Node: "node-3", Process: "tailscaled", Proto: "udp", Why: whyTailscale},
	{Node: "node-3", Process: "tailscaled", Proto: "tcp", Net: tailnetNet, Why: whyTailscale},
	{Node: "node-2", Process: "rpcbind", Proto: "tcp", Port: 111, Why: whyRPCBind},
	{Node: "node-2", Process: "rpcbind", Proto: "udp", Port: 111, Why: whyRPCBind},
}

// StagenetHostUFWRules are the operator's untagged ufw rules, on every node.
var StagenetHostUFWRules = []HostUFWRule{
	{To: "Anywhere on tailscale0", Why: whyTailscale},
}

// StagenetHostListener returns the declaration that covers a listener on node.
func StagenetHostListener(node, proto, addr string, port int, process string) (HostListener, bool) {
	for _, h := range StagenetHostListeners {
		if (h.Node == "" || h.Node == node) && h.Process == process && strings.HasPrefix(proto, h.Proto) && (h.Port == 0 || h.Port == port) && h.covers(addr) {
			return h, true
		}
	}
	return HostListener{}, false
}

// covers reports whether addr lies in the declaration's network; a declaration
// without one covers any address.
func (h HostListener) covers(addr string) bool {
	if h.Net == "" {
		return true
	}
	ip, err := netip.ParseAddr(addr)
	if err != nil {
		return false
	}
	return netip.MustParsePrefix(h.Net).Contains(ip.Unmap())
}

// StagenetHostListenersOn returns every listener declaration for node.
func StagenetHostListenersOn(node string) []HostListener {
	var out []HostListener
	for _, h := range StagenetHostListeners {
		if h.Node == "" || h.Node == node {
			out = append(out, h)
		}
	}
	return out
}

// StagenetHostUFWRule returns the declaration that covers an untagged rule
// whose To column is to.
func StagenetHostUFWRule(node, to string) (HostUFWRule, bool) {
	for _, r := range StagenetHostUFWRulesOn(node) {
		if r.To == to {
			return r, true
		}
	}
	return HostUFWRule{}, false
}

// StagenetHostUFWRulesOn returns every ufw rule declaration for node.
func StagenetHostUFWRulesOn(node string) []HostUFWRule {
	var out []HostUFWRule
	for _, r := range StagenetHostUFWRules {
		if r.Node == "" || r.Node == node {
			out = append(out, r)
		}
	}
	return out
}
