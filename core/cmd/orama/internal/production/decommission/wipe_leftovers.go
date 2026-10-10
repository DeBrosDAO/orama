package decommission

import (
	"path/filepath"
	"strconv"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/archivetrust"
	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/globalnetns"
	"github.com/DeBrosOfficial/network/pkg/install"
	"github.com/DeBrosOfficial/network/pkg/install/installers"
	"github.com/DeBrosOfficial/network/pkg/privhelper"
)

// maxSystemUID is the first uid that is not a system account: useradd --system
// hands out the ones below it.
const maxSystemUID = 1000

// wipedPaths are the files and directories every wipe removes. The leftover check
// looks at the same list, so a path added to the removal and not to the check, or the
// other way round, fails the guard test.
func wipedPaths() []string {
	return []string{
		install.OramaBase, "/etc/orama", "/etc/wireguard/wg0.conf",
		"/var/lib/orama-unit-env", "/var/lib/orama-deploy", "/var/lib/orama-autoupdate",
		"/var/lib/caddy", "/var/lib/ntfy", "/etc/ntfy", "/etc/coredns", "/etc/caddy",
		filepath.Dir(constants.TorConfigPath), installers.TorDataDir,
		filepath.Join(systemdUnitDir, "coredns.service"), filepath.Join(systemdUnitDir, "caddy.service"),
		archivetrust.AnchorPath, privhelper.Path,
	}
}

// globalPaths are what `orama global install` writes outside the unit directory: its
// own binaries (the CLI the chain unit runs, oramad, cosmovisor, Kubo), the state of
// the chain (its keys among it), public storage and the relay, and the configuration
// and sysctl file of its network namespace. Every wipe removes them: a machine that
// kept them was set up again on the old chain's binaries and keys, and orama global
// install refused it (live stagenet create run, 2026-10-10).
func globalPaths() []string {
	return []string{
		constants.GlobalStateRoot, filepath.Dir(constants.GlobalBinDir),
		globalnetns.ConfigDir, globalnetns.SysctlFile,
	}
}

// nuclearPaths are the shared binaries and the Tor apt source that only --nuclear removes.
func nuclearPaths() []string {
	return []string{
		"/usr/local/bin/orama", "/usr/local/bin/orama-node", "/usr/local/bin/gateway",
		"/usr/local/bin/identity", "/usr/local/bin/sfu", "/usr/local/bin/turn", "/usr/local/bin/orama-sni-router",
		"/usr/local/bin/olric-server", "/usr/local/bin/ipfs", "/usr/local/bin/ipfs-cluster-service",
		"/usr/local/bin/rqlited", "/usr/local/bin/coredns", "/usr/local/bin/ntfy", "/usr/bin/caddy",
		installers.TorAptSourcePath, installers.TorKeyringPath,
	}
}

// purgeAccountsBlock is the part of the nuclear wipe that deletes the system accounts
// Orama created: the orama user, the isolated service accounts and the global ones
// (orama-*, whose groups include orama-sfu and orama-ipfs-pub-rpc), and the ntfy user.
// Only accounts below the first regular uid are touched, and only with --nuclear: the
// accounts are shared by every install on the machine, and a reinstall recreates them.
func purgeAccountsBlock() string {
	return `    # The system accounts Orama created. Their processes go first, or userdel refuses.
    orama_accounts() {
        getent passwd | while IFS=: read -r name _ uid _; do
            case "$name" in orama|ntfy|orama-*) [ "$uid" -lt ` + strconv.Itoa(maxSystemUID) + ` ] && echo "$name" ;; esac
        done
    }
    orama_groups() {
        getent group | while IFS=: read -r name _ gid _; do
            case "$name" in orama|ntfy|orama-*) [ "$gid" -lt ` + strconv.Itoa(maxSystemUID) + ` ] && echo "$name" ;; esac
        done
    }
    for name in $(orama_accounts); do
        pkill -9 -u "$name" 2>/dev/null || true
        userdel "$name" || true
    done
    for name in $(orama_groups); do
        groupdel "$name" || true
    done`
}

// leftoverCheck is the end of the wipe script: it lists what of Orama is still on the
// machine and fails when anything is, so a wipe never reports success over state it could
// not remove. What --nuclear alone removes is checked only with --nuclear.
func leftoverCheck(nuclear bool) string {
	paths := append(wipedPaths(), globalPaths()...)
	if nuclear {
		paths = append(paths, nuclearPaths()...)
	}
	var b strings.Builder
	b.WriteString(`# What is left. Anything orama-related still here is listed, and the wipe fails.
leftovers=0
left() { echo "  LEFTOVER: $*" >&2; leftovers=$((leftovers + 1)); }
for path in ` + strings.Join(paths, " ") + `; do
    if [ -e "$path" ] || [ -L "$path" ]; then left "$path"; fi
done
for path in $(find /etc/systemd/system -maxdepth 2 -name "orama-*" 2>/dev/null); do left "$path"; done
for unit in $(systemctl list-units --all --plain --no-legend "orama-*" | while read -r name _; do echo "$name"; done); do
    left "systemd unit $unit"
done
if ip link show wg0 >/dev/null 2>&1; then left "network interface wg0"; fi
if ip netns list 2>/dev/null | grep -qw ` + globalnetns.Name + `; then left "network namespace ` + globalnetns.Name + `"; fi
if ip link show ` + globalnetns.HostIface + ` >/dev/null 2>&1; then left "network interface ` + globalnetns.HostIface + `"; fi
if nft list table ip ` + globalnetns.HostTable + ` >/dev/null 2>&1; then left "nft table ip ` + globalnetns.HostTable + `"; fi
if iptables -C INPUT -i wg0 -s ` + constants.WireGuardSubnet + ` -j ACCEPT 2>/dev/null; then left "iptables INPUT rule for wg0"; fi
if [ -z "${ssh_ports// /}" ]; then
    if ufw status 2>/dev/null | grep -q "# orama"; then left "ufw rules tagged orama (the ports sshd listens on are unknown, so none were removed)"; fi
else
    for num in $(orama_rule_numbers); do left "ufw rule number $num (tagged orama)"; done
fi
`)
	if nuclear {
		b.WriteString(`for name in $(orama_accounts); do left "system user $name"; done
for name in $(orama_groups); do left "system group $name"; done
`)
	}
	b.WriteString(`if [ "$leftovers" -gt 0 ]; then
    echo "  wipe INCOMPLETE: $leftovers orama-related item(s) could not be removed (listed above)" >&2
    exit 1
fi
echo "  Node wiped"`)
	return b.String()
}
