// Package globalnetns is the network-namespace layout that lets a cluster node
// and a global node share one machine. The global services run inside the
// orama-global network namespace, joined to the root namespace by a veth pair;
// the cluster node stays in the root namespace. Inside the namespace,
// 127.0.0.1 is the global services' own loopback, so the cluster's loopback
// (Kubo RPC, the gateway's loopback trust, the Caddy admin socket) is not
// reachable from a global unit, and the namespace has its own port space.
//
// The package only renders text (a unit, two nftables rulesets, a
// resolv.conf), checks the machine, and checks that a layout is installed. It
// runs nothing itself: `orama global install --colocated` writes what it
// renders and systemd applies it.
package globalnetns

import (
	"fmt"
	"slices"
	"strings"
)

// The layout. The veth pair uses 198.18.0.0/30 (RFC 2544 benchmarking space):
// it is in none of the ranges the global units deny (RFC 1918, link-local,
// CGNAT), and no provider routes it.
const (
	// Name is the network namespace, and the value preferences.yaml records
	// as global_netns.
	Name = "orama-global"
	// Path is where `ip netns add` bind-mounts the namespace; units join it
	// with NetworkNamespacePath=.
	Path = "/run/netns/" + Name

	// HostIface is the veth end in the root namespace, NSIface the end inside.
	// Both are under IFNAMSIZ (15).
	HostIface = "ogl-host"
	NSIface   = "ogl-ns"

	HostAddr = "198.18.0.1"
	NSAddr   = "198.18.0.2"
	Subnet   = "198.18.0.0/30"

	// UnitName is the oneshot that builds and tears down the namespace. Every
	// global unit is BindsTo it.
	UnitName = "orama-global-netns.service"

	// ConfigDir holds the rulesets and resolv.conf, root-owned.
	ConfigDir     = "/etc/orama-global"
	HostRulesFile = ConfigDir + "/netns-host.nft"
	NSRulesFile   = ConfigDir + "/netns.nft"
	ResolvFile    = ConfigDir + "/resolv.conf"
	// SysctlFile makes the kernel forward between the veth and the public
	// interface. Forwarding is confined by the rulesets, not by leaving it off.
	SysctlFile = "/etc/sysctl.d/60-orama-global-netns.conf"

	hostTable = "orama_global"
	nsTable   = "orama_global_ns"
)

// Resolvers are what the namespace's resolv.conf names. The namespace cannot
// reach the host's stub resolver (127.0.0.53 is the host's loopback), and it
// may not reach a resolver on a private network.
var Resolvers = []string{"9.9.9.9", "1.1.1.1"}

// PrivateRanges are the networks nothing in the namespace may reach: the
// WireGuard mesh (10.0.0.0/24 is inside 10/8), other tenants' networks, the
// cloud metadata address, and CGNAT. It is the same list the global units'
// IPAddressDeny= holds, enforced a second time in the kernel firewall.
var PrivateRanges = []string{
	"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "169.254.0.0/16", "100.64.0.0/10",
}

// Port is a public listener of a global service.
type Port struct {
	Proto  string // tcp or udp
	Number int
}

// Tools are the absolute paths of the binaries the namespace unit runs.
type Tools struct {
	IP     string
	Nft    string
	Sysctl string
}

// Layout is one machine's co-location: which ports are published into the
// namespace and which binaries build it.
type Layout struct {
	Ports []Port
	Tools Tools
}

// Validate refuses a layout the renderers cannot express safely: every value
// ends up in a unit or a ruleset.
func (l Layout) Validate() error {
	for name, path := range map[string]string{"ip": l.Tools.IP, "nft": l.Tools.Nft, "sysctl": l.Tools.Sysctl} {
		if !strings.HasPrefix(path, "/") || strings.ContainsAny(path, " \t\n\"'\\$%;") {
			return fmt.Errorf("the %s binary path %q is not a plain absolute path", name, path)
		}
	}
	for _, p := range l.Ports {
		if p.Proto != "tcp" && p.Proto != "udp" {
			return fmt.Errorf("port protocol %q is not tcp or udp", p.Proto)
		}
		if p.Number < 1 || p.Number > 65535 {
			return fmt.Errorf("port %d is not a TCP/UDP port", p.Number)
		}
	}
	return nil
}

// portSet is `{ 1, 2 }` for the ports of proto, or "" when there are none.
func (l Layout) portSet(proto string) string {
	var nums []int
	for _, p := range l.Ports {
		if p.Proto == proto && !slices.Contains(nums, p.Number) {
			nums = append(nums, p.Number)
		}
	}
	if len(nums) == 0 {
		return ""
	}
	slices.Sort(nums)
	parts := make([]string, len(nums))
	for i, n := range nums {
		parts[i] = fmt.Sprint(n)
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}

func privateSet() string { return "{ " + strings.Join(PrivateRanges, ", ") + " }" }

// RenderResolvConf is the namespace's /etc/resolv.conf, bind-mounted over the
// host's for each global unit.
func RenderResolvConf() string {
	var b strings.Builder
	for _, r := range Resolvers {
		b.WriteString("nameserver " + r + "\n")
	}
	return b.String()
}

// RenderSysctl turns on IPv4 forwarding.
func RenderSysctl() string {
	return "# Written by orama global install --colocated; the rulesets in " + ConfigDir + " confine what is forwarded.\nnet.ipv4.ip_forward = 1\n"
}

// RenderHostRules is the ruleset loaded in the root namespace. It publishes
// the listed ports into the namespace with DNAT, masquerades the namespace's
// outbound traffic, refuses it any private destination, refuses everything
// arriving from the veth at the host itself (so the namespace cannot reach a
// cluster service through the host's public or WireGuard address), and
// forwards into the namespace only DNAT'd connections and their replies.
func (l Layout) RenderHostRules() string {
	var b strings.Builder
	fmt.Fprintf(&b, "table ip %s {\n", hostTable)
	b.WriteString("\tchain prerouting {\n\t\ttype nat hook prerouting priority dstnat; policy accept;\n")
	for _, proto := range []string{"tcp", "udp"} {
		if set := l.portSet(proto); set != "" {
			fmt.Fprintf(&b, "\t\tiifname != %q fib daddr type local %s dport %s dnat to %s\n", HostIface, proto, set, NSAddr)
		}
	}
	b.WriteString("\t}\n")
	fmt.Fprintf(&b, "\tchain postrouting {\n\t\ttype nat hook postrouting priority srcnat; policy accept;\n\t\tip saddr %s oifname != %q masquerade\n\t}\n", Subnet, HostIface)
	fmt.Fprintf(&b, "\tchain forward {\n\t\ttype filter hook forward priority -1; policy accept;\n")
	fmt.Fprintf(&b, "\t\tiifname %q ip daddr %s drop\n", HostIface, privateSet())
	fmt.Fprintf(&b, "\t\toifname %q ct state established,related accept\n", HostIface)
	fmt.Fprintf(&b, "\t\toifname %q ct status dnat accept\n", HostIface)
	fmt.Fprintf(&b, "\t\toifname %q drop\n\t}\n", HostIface)
	fmt.Fprintf(&b, "\tchain input {\n\t\ttype filter hook input priority -1; policy accept;\n")
	fmt.Fprintf(&b, "\t\tiifname %q ct state established,related accept\n", HostIface)
	fmt.Fprintf(&b, "\t\tiifname %q drop\n\t}\n}\n", HostIface)
	return b.String()
}

// RenderNSRules is the ruleset loaded inside the namespace: nothing arrives
// but replies, loopback and the published ports, nothing is forwarded, and
// nothing leaves for a private network.
func (l Layout) RenderNSRules() string {
	var b strings.Builder
	fmt.Fprintf(&b, "table ip %s {\n", nsTable)
	b.WriteString("\tchain input {\n\t\ttype filter hook input priority 0; policy drop;\n")
	b.WriteString("\t\tiifname \"lo\" accept\n\t\tct state established,related accept\n")
	for _, proto := range []string{"tcp", "udp"} {
		if set := l.portSet(proto); set != "" {
			fmt.Fprintf(&b, "\t\t%s dport %s accept\n", proto, set)
		}
	}
	b.WriteString("\t}\n")
	b.WriteString("\tchain forward {\n\t\ttype filter hook forward priority 0; policy drop;\n\t}\n")
	fmt.Fprintf(&b, "\tchain output {\n\t\ttype filter hook output priority 0; policy accept;\n\t\tct state established,related accept\n\t\tip daddr %s drop\n\t}\n}\n", privateSet())
	return b.String()
}

// RenderUnit is orama-global-netns.service: a oneshot that builds the
// namespace, the veth pair, the addresses, the default route and both
// rulesets, and takes them down again on stop. It has no sandboxing, on
// purpose: `ip netns add` must bind-mount into the host's mount namespace.
// The Pre lines clear what an unclean shutdown left behind.
func (l Layout) RenderUnit() string {
	ip, nft := l.Tools.IP, l.Tools.Nft
	var b strings.Builder
	b.WriteString("[Unit]\nDescription=Orama global network namespace\nAfter=network-online.target\nWants=network-online.target\n\n[Service]\nType=oneshot\nRemainAfterExit=yes\n")
	pre := []string{
		ip + " netns del " + Name,
		ip + " link del " + HostIface,
		nft + " delete table ip " + hostTable,
	}
	for _, c := range pre {
		b.WriteString("ExecStartPre=-" + c + "\n")
	}
	start := []string{
		l.Tools.Sysctl + " -q -w net.ipv4.ip_forward=1",
		ip + " netns add " + Name,
		ip + " link add " + HostIface + " type veth peer name " + NSIface,
		ip + " link set " + NSIface + " netns " + Name,
		ip + " addr add " + HostAddr + "/30 dev " + HostIface,
		ip + " link set " + HostIface + " up",
		ip + " -n " + Name + " addr add " + NSAddr + "/30 dev " + NSIface,
		ip + " -n " + Name + " link set " + NSIface + " up",
		ip + " -n " + Name + " link set lo up",
		ip + " -n " + Name + " route add default via " + HostAddr,
		nft + " -f " + HostRulesFile,
		ip + " netns exec " + Name + " " + nft + " -f " + NSRulesFile,
	}
	for _, c := range start {
		b.WriteString("ExecStart=" + c + "\n")
	}
	stop := []string{
		nft + " delete table ip " + hostTable,
		ip + " link del " + HostIface,
		ip + " netns del " + Name,
	}
	for _, c := range stop {
		b.WriteString("ExecStop=-" + c + "\n")
	}
	b.WriteString("\n[Install]\nWantedBy=multi-user.target\n")
	return b.String()
}

// unitAnchors are the two lines ApplyToUnit inserts after; every global unit
// from renderGlobalUnitExtra has both.
const (
	unitSectionAnchor    = "StartLimitIntervalSec=0\n"
	serviceSectionAnchor = "Type=simple\n"
)

// ApplyToUnit puts a global unit inside the namespace: it is bound to the
// namespace unit (it stops when the namespace does, and never runs without
// one), joins the namespace with NetworkNamespacePath=, and sees the
// namespace's resolv.conf instead of the host's.
func ApplyToUnit(unit string) (string, error) {
	if strings.Contains(unit, "NetworkNamespacePath=") {
		return "", fmt.Errorf("the unit already joins a network namespace")
	}
	if strings.Count(unit, unitSectionAnchor) != 1 || strings.Count(unit, serviceSectionAnchor) != 1 {
		return "", fmt.Errorf("the unit has no %q and %q line to anchor the namespace settings on", strings.TrimSpace(unitSectionAnchor), strings.TrimSpace(serviceSectionAnchor))
	}
	unit = strings.Replace(unit, unitSectionAnchor, unitSectionAnchor+"BindsTo="+UnitName+"\nAfter="+UnitName+"\n", 1)
	return strings.Replace(unit, serviceSectionAnchor,
		serviceSectionAnchor+"NetworkNamespacePath="+Path+"\nBindReadOnlyPaths="+ResolvFile+":/etc/resolv.conf\n", 1), nil
}
