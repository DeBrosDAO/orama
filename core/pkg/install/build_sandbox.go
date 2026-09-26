package install

import (
	"fmt"
	"net"
	"net/netip"
	"path/filepath"
	"sort"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/rootfs"
)

// orama-deploy-build@ runs a tenant's `npm install` with the network, and its
// template (owned with the deployments) denies loopback and the private ranges.
// Two things it cannot say for itself depend on the node, so install and
// upgrade write them:
//
//   - The node's own public addresses. A packet to one of them never leaves
//     the host: it is routed over lo, and UFW accepts everything on lo — so
//     the build reached every service the node listens on publicly or on all
//     interfaces, the firewall notwithstanding. A drop-in for the template
//     denies them (buildSandboxDropIn); it is rewritten on every install and
//     upgrade, so a changed public_ip is picked up there.
//   - A resolver it may use. It cannot reach systemd-resolved's stub on
//     127.0.0.53, which is loopback; the template binds
//     /etc/orama/build-resolv.conf over /etc/resolv.conf, and that file names
//     public resolvers only (buildResolvConf).

// buildUnitDropInDir is the build template's drop-in directory, in root-owned
// /etc/systemd/system; a drop-in for a template applies to every instance.
const buildUnitDropInDir = "/etc/systemd/system/orama-deploy-build@.service.d"

// buildUnitDropInName is the drop-in that denies the node's public addresses.
const buildUnitDropInName = "deny-public.conf"

// BuildResolvConfPath is the resolver configuration the build unit binds over
// /etc/resolv.conf.
const BuildResolvConfPath = "/etc/orama/build-resolv.conf"

// Public resolvers the build sandbox uses. Three operators, so one being down
// or filtering a registry does not stop a build: Cloudflare, Quad9 and Google.
// None is a private or loopback address, which the build unit denies.
const (
	buildResolverCloudflare = "1.1.1.1"
	buildResolverQuad9      = "9.9.9.9"
	buildResolverGoogle     = "8.8.8.8"
)

// buildResolvConf is BuildResolvConfPath's content: the resolvers and nothing
// else — no search or domain line, which would send a build's lookups for
// short names to the cluster's own zone.
func buildResolvConf() string {
	var sb strings.Builder
	sb.WriteString("# Written by the Orama installer on install and upgrade (pkg/install/build_sandbox.go) for orama-deploy-build@.\n")
	for _, ns := range []string{buildResolverCloudflare, buildResolverQuad9, buildResolverGoogle} {
		sb.WriteString("nameserver " + ns + "\n")
	}
	return sb.String()
}

// buildSandboxDropIn is the drop-in that denies addrs to the build unit.
func buildSandboxDropIn(addrs []netip.Addr) string {
	var sb strings.Builder
	sb.WriteString("# Written by the Orama installer on install and upgrade (pkg/install/build_sandbox.go): this node's own\n")
	sb.WriteString("# public addresses, which a build would otherwise reach over lo.\n[Service]\n")
	if len(addrs) > 0 {
		parts := make([]string, len(addrs))
		for i, a := range addrs {
			parts[i] = netip.PrefixFrom(a, a.BitLen()).String()
		}
		sb.WriteString("IPAddressDeny=" + strings.Join(parts, " ") + "\n")
	}
	return sb.String()
}

// hostPublicAddrs is publicIP and every globally routable unicast address on
// the host's interfaces, deduplicated and sorted. Private, loopback, link-local
// and carrier-grade NAT addresses are left out: the build template denies those
// ranges already.
func hostPublicAddrs(publicIP string, ifaceAddrs []netip.Addr) ([]netip.Addr, error) {
	seen := map[netip.Addr]bool{}
	if publicIP != "" {
		ip, err := netip.ParseAddr(publicIP)
		if err != nil {
			return nil, fmt.Errorf("node public_ip %q is not an IP address: %w", publicIP, err)
		}
		seen[ip.Unmap()] = true
	}
	for _, a := range ifaceAddrs {
		a = a.Unmap()
		if a.IsGlobalUnicast() && !a.IsPrivate() && !cgnat.Contains(a) {
			seen[a] = true
		}
	}
	out := make([]netip.Addr, 0, len(seen))
	for a := range seen {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Less(out[j]) })
	return out, nil
}

// cgnat is RFC 6598 shared address space, which the build template denies.
var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// interfaceAddrs is every address on the host's interfaces.
func interfaceAddrs() ([]netip.Addr, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, fmt.Errorf("list this host's addresses: %w", err)
	}
	var out []netip.Addr
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok {
			if ip, ok := netip.AddrFromSlice(ipnet.IP); ok {
				out = append(out, ip)
			}
		}
	}
	return out, nil
}

// installBuildSandbox writes the build unit's drop-in and resolver file, both
// through rootfs anchored at /etc so a symlink under it is refused. The caller
// reloads systemd.
func installBuildSandbox(publicIP string) error {
	ifaces, err := interfaceAddrs()
	if err != nil {
		return err
	}
	addrs, err := hostPublicAddrs(publicIP, ifaces)
	if err != nil {
		return err
	}
	etc := rootfs.At("/etc")
	if err := etc.MkdirAll(buildUnitDropInDir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", buildUnitDropInDir, err)
	}
	dropIn := filepath.Join(buildUnitDropInDir, buildUnitDropInName)
	if err := etc.WriteFile(dropIn, []byte(buildSandboxDropIn(addrs)), 0o644); err != nil {
		return fmt.Errorf("write the build sandbox drop-in %s: %w", dropIn, err)
	}
	if err := etc.MkdirAll(filepath.Dir(BuildResolvConfPath), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(BuildResolvConfPath), err)
	}
	if err := etc.WriteFile(BuildResolvConfPath, []byte(buildResolvConf()), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", BuildResolvConfPath, err)
	}
	return nil
}
