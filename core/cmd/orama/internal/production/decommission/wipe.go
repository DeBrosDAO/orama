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
	"github.com/DeBrosOfficial/network/pkg/archivetrust"
	"github.com/DeBrosOfficial/network/pkg/releaseverify"
	"github.com/DeBrosOfficial/network/pkg/updatenotice"
	"path/filepath"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/inspector"
	"github.com/DeBrosOfficial/network/pkg/install"
	"github.com/DeBrosOfficial/network/pkg/install/installers"
	"github.com/DeBrosOfficial/network/pkg/privhelper"
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

# The auto-update timer first: an install in progress would otherwise restart
# the services this script is stopping.
systemctl stop orama-autoupdate.timer orama-autoupdate.service 2>/dev/null
systemctl disable orama-autoupdate.timer 2>/dev/null

# Stop every namespace unit FIRST. These are template instances
# (orama-namespace-rqlite@index, ...@<tenant>) and match none of the legacy
# host unit names below, so they used to keep running under a deleted data dir.
for unit in $(systemctl list-units --all --plain --no-legend "orama-namespace-*" "orama-deploy-*" | awk "{print \$1}"); do
    systemctl stop "$unit" 2>/dev/null
    systemctl disable "$unit" 2>/dev/null
done
systemctl stop "orama-namespace-*@*.service" 2>/dev/null || true

# The privileged helper: stop its socket so nothing can reach root through it
# while the rest is torn down. Its unit files and binary go below.
systemctl stop %[9]s 2>/dev/null
systemctl disable %[9]s 2>/dev/null

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
rm -f %[10]s
# Everything else orama-named under the unit directory: the drop-in directories of
# deployments (orama-deploy-<kind>@<ns>.service.d), the templates the units are
# instantiated from, and the .wants symlinks a unit left in a target.
find /etc/systemd/system -maxdepth 2 -name "orama-*" -exec rm -rf {} + 2>/dev/null
rm -f /etc/orama/build-resolv.conf
systemctl daemon-reload 2>/dev/null
# A unit that outlives its file ("not-found active exited": a transient namespace
# unit such as orama-namespace-wireguard@index) is stopped and forgotten here.
for unit in $(systemctl list-units --all --plain --no-legend "orama-*" | while read -r name _; do echo "$name"; done); do
    systemctl stop "$unit" 2>/dev/null
    systemctl reset-failed "$unit" 2>/dev/null
done
systemctl daemon-reload 2>/dev/null
systemctl reset-failed 2>/dev/null || true

# Tear down WireGuard
ip link delete wg0 2>/dev/null || true
rm -f /etc/wireguard/wg0.conf
# The accept rule the installer put ahead of ufw for the mesh (pkg/install firewall).
while iptables -D INPUT -i wg0 -s %[15]s -j ACCEPT 2>/dev/null; do :; done

# Remove the firewall rules Orama added — tagged "orama" (the cluster) or
# "orama-global" (a global node, route rules included) — newest first so the
# numbers stay valid. Rules the operator added (a tailscale0 allow, a monitoring
# port) belong to them, and a reset used to delete them. The rules for the ports
# sshd listens on stay, whoever added them: this script runs over SSH. ufw itself
# stays as it is, enabled with those rules, so the machine stays reachable.
ssh_ports=" $(sshd -T 2>/dev/null | while read -r key val; do [ "$key" = port ] && printf "%%s/tcp " "$val"; done)"
orama_rule_numbers() {
    ufw status numbered 2>/dev/null | while IFS= read -r line; do
        case "$line" in "["*"# orama"|"["*"# orama-global") ;; *) continue ;; esac
        num=${line#[}; num=${num%%%%]*}; num=${num// /}
        rest=${line#*] }; rule=${rest%%%% *}
        case "$ssh_ports" in *" $rule "*) continue ;; esac
        echo "$num"
    done
}
if [ -z "${ssh_ports// /}" ]; then
    echo "  cannot read the ports sshd listens on (sshd -T); leaving the firewall rules untouched" >&2
else
    orama_rule_numbers | sort -rn | while read -r num; do ufw --force delete "$num" >/dev/null; done
fi

# Remove data
rm -rf /opt/orama
# Root-owned env files of namespace units and deployments (orama-privhelper),
# plus the per-deployment state and cache systemd keeps for DynamicUser units.
rm -rf /var/lib/orama-unit-env /var/lib/orama-deploy
rm -rf /var/lib/private/orama-deploy-* /var/cache/private/orama-deploy-* /var/cache/private/orama-build
rm -rf /var/lib/ntfy /run/ntfy /etc/ntfy
# Fetched releases and the install intent of the auto-update agent.
rm -rf /var/lib/orama-autoupdate
# Caddy storage: the TLS private keys of the node and its ACME account key.
# A wiped node that kept them would serve the old certificate and hold keys for
# a domain it no longer belongs to.
rm -rf /var/lib/caddy
rm -rf /var/log/journal
swapoff -a 2>/dev/null || true

# The privileged helper binary: root-owned, and useless without its units.
rm -f %[11]s

# The global layer (orama global install): its binaries, the chain with its keys,
# public storage, the relay, and the configuration and sysctl file of its namespace.
# The units went above; stopping the namespace unit deleted the namespace, its
# veth and its nft table (its ExecStop), and the check below looks for each.
rm -rf %[16]s

# Clean configs
rm -rf /etc/coredns
rm -rf /etc/caddy
rm -rf %[5]s
# The archive trust anchor and its rotation mark: a machine wiped and set up
# again must trust the cluster it next joins or creates, not this one.
rm -f %[12]s
rm -f /tmp/orama-*.sh /tmp/network-source.tar.gz /tmp/orama-*.tar.gz
# The configuration directory itself, empty by now (the anchor, the Tor config
# and the build resolver file lived in it).
rm -rf /etc/orama

# Nuclear: remove binaries, and Tor with its apt source. The distro Tor units
# stay masked otherwise, so a reinstall never finds tor@default on the port.
if [ -n "$NUCLEAR" ]; then
    rm -f /usr/local/bin/orama /usr/local/bin/orama-node /usr/local/bin/gateway
    rm -f /usr/local/bin/identity /usr/local/bin/sfu /usr/local/bin/turn /usr/local/bin/orama-sni-router
    rm -f /usr/local/bin/olric-server /usr/local/bin/ipfs /usr/local/bin/ipfs-cluster-service
    rm -f /usr/local/bin/rqlited /usr/local/bin/coredns /usr/local/bin/ntfy
    rm -f /usr/bin/caddy
    DEBIAN_FRONTEND=noninteractive apt-get purge -y %[6]s 2>/dev/null || true
    rm -f %[7]s
    systemctl unmask %[8]s 2>/dev/null || true
%[13]s
fi

%[14]s
'`, nuclearFlag,
		strings.Join(installers.LegacyAnyoneUnits, " "),
		strings.Join(installers.LegacyAnyonePaths(install.OramaDir), " "),
		strings.Join(installers.LegacyAnyonePackages, " "),
		strings.Join([]string{filepath.Dir(constants.TorConfigPath), installers.TorDataDir}, " "),
		strings.Join(installers.TorAptPackages, " "),
		strings.Join([]string{installers.TorAptSourcePath, installers.TorKeyringPath}, " "),
		strings.Join(installers.TorDistroUnits, " "),
		privhelper.SocketUnitName,
		strings.Join([]string{
			filepath.Join(systemdUnitDir, privhelper.SocketUnitName),
			filepath.Join(systemdUnitDir, privhelper.ServiceUnitName),
		}, " "),
		privhelper.Path,
		strings.Join(append([]string{
			archivetrust.AnchorPath, archivetrust.RotationMarkPath(archivetrust.AnchorPath), updatenotice.Path,
		}, releaseverify.NodeStatePaths()...), " "),
		purgeAccountsBlock(),
		leftoverCheck(nuclear),
		constants.WireGuardSubnet,
		strings.Join(globalPaths(), " "),
	)
}

// systemdUnitDir is where the installer writes unit files.
const systemdUnitDir = "/etc/systemd/system"

// wipeNode erases the target node.
func wipeNode(node inspector.Node, nuclear bool) error {
	if err := remotessh.RunSSHStreaming(node, remotessh.SudoPrefix(node)+wipeScript(nuclear)); err != nil {
		return fmt.Errorf("the wipe script failed (when it removed all it could and Orama state remains, each item is listed above as LEFTOVER): %w", err)
	}
	return nil
}
