// Package decommission removes a node from the cluster and, optionally, erases
// it.
//
// The two halves are deliberately separate commands. `decommission` runs on a
// SURVIVOR and retires the node from every store the cluster keeps; `wipe` runs
// on the TARGET and erases it. `clean` did only the second, which is why a
// deleted VPS left a configured raft voter, a WireGuard peer and a dns_nodes
// row behind on every node that was still running.
package decommission

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/inspector"
	"github.com/DeBrosOfficial/network/pkg/install"
	"github.com/DeBrosOfficial/network/pkg/install/installers"
	"github.com/DeBrosOfficial/network/pkg/remotessh"
)

// wipeScript erases an Orama installation from a node.
//
// Two fixes over the script it replaces:
//
//   - It stops every `orama-namespace-*@*` unit before removing anything.
//     `clean` stopped only the legacy host unit names, so tenant units — which
//     are template instances and match none of those names — kept running
//     under a data directory that had just been deleted, writing into unlinked
//     files until something noticed.
//
//   - `pkill -9 -f "ipfs"` is anchored. Unanchored, that pattern matches any
//     command line containing the substring "ipfs" — including an operator's
//     own `tail -f .../ipfs.log`, an editor with an ipfs config open, or a
//     grep. On a node that is being wiped that is survivable; the habit is not.
func wipeScript(nuclear bool) string {
	nuclearFlag := ""
	if nuclear {
		nuclearFlag = "NUCLEAR=1"
	}

	return fmt.Sprintf(`bash -c '
%[1]s

# Stop every namespace unit FIRST. These are template instances
# (orama-namespace-rqlite@index, ...@<tenant>) and match none of the legacy
# host unit names below, so they used to keep running under a deleted data dir.
for unit in $(systemctl list-units --all --plain --no-legend "orama-namespace-*" | awk "{print \$1}"); do
    systemctl stop "$unit" 2>/dev/null
    systemctl disable "$unit" 2>/dev/null
done
systemctl stop "orama-namespace-*@*.service" 2>/dev/null || true

# Then the supervisor and the legacy host units.
for svc in orama-node orama-turn orama-sni-router caddy coredns ntfy \
           orama-gateway orama-ipfs-cluster orama-ipfs orama-olric orama-vault; do
    systemctl stop "$svc" 2>/dev/null
    systemctl disable "$svc" 2>/dev/null
done

# The removed Anyone network, on a node that was never upgraded past it:
# units, package, apt source, config, state (relay keys included) and logs.
for svc in %[2]s; do
    systemctl stop "$svc" 2>/dev/null
    systemctl disable "$svc" 2>/dev/null
done
for pkg in %[4]s; do
    DEBIAN_FRONTEND=noninteractive apt-get purge -y "$pkg" 2>/dev/null || true
done
rm -rf %[3]s

# Kill stragglers. Every pattern is anchored to a full path or a binary name so
# it cannot match an unrelated command line that merely mentions the word.
for pattern in /opt/orama/bin/orama-node /opt/orama/bin/gateway /opt/orama/bin/turn \
               /opt/orama/bin/sfu /opt/orama/bin/vault-guardian /opt/orama/bin/orama-sni-router \
               /usr/local/bin/olric-server /usr/local/bin/rqlited \
               /usr/local/bin/ipfs /usr/local/bin/ipfs-cluster-service; do
    pkill -9 -x -f "$pattern.*" 2>/dev/null || true
done

# Remove systemd units
rm -f /etc/systemd/system/orama-*.service
rm -f /etc/systemd/system/orama-*.timer
rm -f /etc/systemd/system/coredns.service
rm -f /etc/systemd/system/caddy.service
systemctl daemon-reload 2>/dev/null
systemctl reset-failed 2>/dev/null || true

# Tear down WireGuard
ip link delete wg0 2>/dev/null || true
rm -f /etc/wireguard/wg0.conf

# Reset firewall
ufw --force reset 2>/dev/null || true
ufw default deny incoming 2>/dev/null || true
ufw default allow outgoing 2>/dev/null || true
ufw allow 22/tcp 2>/dev/null || true
ufw --force enable 2>/dev/null || true

# Remove data
rm -rf /opt/orama
rm -rf /var/lib/ntfy /run/ntfy
rm -rf /var/log/journal
swapoff -a 2>/dev/null || true

# Clean configs
rm -rf /etc/coredns
rm -rf /etc/caddy
rm -rf %[5]s
rm -f /tmp/orama-*.sh /tmp/network-source.tar.gz /tmp/orama-*.tar.gz

# Nuclear: remove binaries, and Tor with its apt source. The distro Tor units
# stay masked otherwise, so a reinstall never finds tor@default on the port.
if [ -n "$NUCLEAR" ]; then
    rm -f /usr/local/bin/orama /usr/local/bin/orama-node /usr/local/bin/gateway
    rm -f /usr/local/bin/identity /usr/local/bin/sfu /usr/local/bin/turn /usr/local/bin/orama-sni-router
    rm -f /usr/local/bin/olric-server /usr/local/bin/ipfs /usr/local/bin/ipfs-cluster-service
    rm -f /usr/local/bin/rqlited /usr/local/bin/coredns
    rm -f /usr/bin/caddy
    DEBIAN_FRONTEND=noninteractive apt-get purge -y %[6]s 2>/dev/null || true
    rm -f %[7]s
    systemctl unmask %[8]s 2>/dev/null || true
fi

echo "  Node wiped"
'`, nuclearFlag,
		strings.Join(installers.LegacyAnyoneUnits, " "),
		strings.Join(installers.LegacyAnyonePaths(install.OramaDir), " "),
		strings.Join(installers.LegacyAnyonePackages, " "),
		strings.Join([]string{filepath.Dir(constants.TorConfigPath), installers.TorDataDir}, " "),
		strings.Join(installers.TorAptPackages, " "),
		strings.Join([]string{installers.TorAptSourcePath, installers.TorKeyringPath}, " "),
		strings.Join(installers.TorDistroUnits, " "),
	)
}

// wipeNode erases the target node.
func wipeNode(node inspector.Node, nuclear bool) error {
	return remotessh.RunSSHStreaming(node, remotessh.SudoPrefix(node)+wipeScript(nuclear))
}
