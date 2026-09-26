package install

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/privhelper"
	"github.com/DeBrosOfficial/network/pkg/wireguard"
)

// The conf file itself is owned by pkg/wireguard, which the privileged helper
// links without the rest of the installer.
type WGConf = wireguard.Conf

// DefaultWGConfPath is the interface config wg-quick reads at boot.
const DefaultWGConfPath = wireguard.DefaultConfPath

// NewWGConf returns a conf owner for path. An empty path means the default.
func NewWGConf(path string) *WGConf { return wireguard.NewConf(path) }

// ReadLiveWGPeers returns the peers currently configured on the interface,
// parsed from `wg show <iface> dump`.
//
// The dump format is machine-readable and carries the endpoint and allowed IPs,
// which `wg show` alone does not expose in a parseable way. The first line
// describes the interface itself and is skipped.
func ReadLiveWGPeers(iface string) (map[string]WireGuardPeer, error) {
	out, err := exec.Command("wg", "show", iface, "dump").Output()
	if err != nil {
		return nil, fmt.Errorf("wg show %s dump: %w", iface, err)
	}
	return parseWGDump(string(out)), nil
}

// parseWGDump turns `wg show <iface> dump` output into peers by public key.
//
// Columns per peer line: public key, preshared key, endpoint, allowed ips,
// latest handshake, rx, tx, keepalive. An endpoint of "(none)" means the peer
// has never been reached and carries no address.
func parseWGDump(dump string) map[string]WireGuardPeer {
	peers := make(map[string]WireGuardPeer)
	for i, line := range strings.Split(strings.TrimSpace(dump), "\n") {
		if i == 0 {
			continue // interface line
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 4 {
			continue
		}
		pubKey := strings.TrimSpace(fields[0])
		if pubKey == "" {
			continue
		}
		endpoint := strings.TrimSpace(fields[2])
		if endpoint == "(none)" {
			endpoint = ""
		}
		// A mesh peer owns exactly one /32. "(none)" is a peer whose address
		// moved to a new key (a node replaced on the same overlay IP), and a
		// list is not a mesh peer: persisting either would be refused as a
		// whole, and wg0.conf would stop being updated at all.
		allowed := strings.TrimSpace(fields[3])
		if allowed == "(none)" || strings.Contains(allowed, ",") {
			continue
		}
		peers[pubKey] = WireGuardPeer{
			PublicKey: pubKey,
			Endpoint:  endpoint,
			AllowedIP: allowed,
		}
	}
	return peers
}

// WGPeerManager applies peers to the running interface and persists the result.
//
// It replaces the zero-value WireGuardProvisioner the sync loop used to build.
// That value had no config directory and no private key, so its writes went to
// a relative path and, had they landed, would have rewritten [Interface] with
// an empty key. This type never renders [Interface] at all - it only ever
// rewrites [Peer] sections of the file that is already there.
type WGPeerManager struct {
	iface string
	conf  *WGConf
}

// NewWGPeerManager returns a manager for the given conf path ("" = default).
func NewWGPeerManager(confPath string) *WGPeerManager {
	return &WGPeerManager{iface: "wg0", conf: NewWGConf(confPath)}
}

// AddPeer applies a peer to the running interface. It is idempotent, so it is
// also how an endpoint or allowed-ips change is rolled out for a known key.
func (m *WGPeerManager) AddPeer(peer WireGuardPeer) error {
	args := []string{"set", m.iface, "peer", peer.PublicKey,
		"allowed-ips", peer.AllowedIP, "persistent-keepalive", "25"}
	if peer.Endpoint != "" {
		args = append(args, "endpoint", peer.Endpoint)
	}
	if output, err := exec.Command("wg", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("wg set peer %s: %w\n%s", peer.AllowedIP, err, string(output))
	}
	return nil
}

// RemovePeer drops a peer from the running interface.
func (m *WGPeerManager) RemovePeer(publicKey string) error {
	output, err := exec.Command("wg", "set", m.iface, "peer", publicKey, "remove").CombinedOutput()
	if err != nil {
		return fmt.Errorf("wg set peer remove: %w\n%s", err, string(output))
	}
	return nil
}

// PersistPeers writes the peer set to wg0.conf. /etc/wireguard is root's
// and must stay so — wg-quick runs the conf's PostUp lines as root — so an
// unprivileged caller hands the peers to orama-privhelper, which validates
// them and rewrites only the [Peer] sections.
func (m *WGPeerManager) PersistPeers(peers []WireGuardPeer) error {
	if os.Geteuid() == 0 {
		return m.conf.PersistPeers(peers)
	}
	return privhelper.PersistWireGuardPeers(peers)
}
