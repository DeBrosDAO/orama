package production

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// DefaultWGConfPath is the interface config wg-quick reads at boot.
const DefaultWGConfPath = "/etc/wireguard/wg0.conf"

// WGConf owns /etc/wireguard/wg0.conf for the running node.
//
// It exists because the peer set on the live interface and the peer set in the
// config file were two different things. The 60s sync applied peers to the
// kernel with `wg set` and then tried to persist them through a provisioner
// built as a zero value: no config directory, no private key, no listen port.
// Every write went to a relative path under a read-only WorkingDirectory and
// failed, and had it succeeded it would have emitted an [Interface] block with
// an empty PrivateKey and a single peer - destroying the file. So wg0.conf only
// ever held the peers written at install time, and every `wg-quick up wg0`
// after a reboot brought the mesh back as it was on day one.
//
// The [Interface] block is preserved verbatim rather than regenerated. This
// process does not hold the private key, and the block also carries wg-quick
// directives (Address, MTU, PostUp/PostDown) that `wg` itself does not model.
// Only [Peer] sections are rewritten.
type WGConf struct {
	path string
}

// NewWGConf returns a conf owner for path. An empty path means the default.
func NewWGConf(path string) *WGConf {
	if path == "" {
		path = DefaultWGConfPath
	}
	return &WGConf{path: path}
}

// Path is the file this owner writes.
func (c *WGConf) Path() string { return c.path }

// PersistPeers rewrites the [Peer] sections to exactly peers, leaving the
// [Interface] block untouched.
//
// Peers are written in sorted AllowedIP order so an unchanged mesh produces a
// byte-identical file and a diff shows only real membership changes.
func (c *WGConf) PersistPeers(peers []WireGuardPeer) error {
	existing, err := os.ReadFile(c.path)
	if err != nil {
		return fmt.Errorf("read %s: %w", c.path, err)
	}

	iface, err := interfaceSection(string(existing))
	if err != nil {
		return fmt.Errorf("%s: %w", c.path, err)
	}

	var sb strings.Builder
	sb.WriteString(strings.TrimRight(iface, "\n"))
	sb.WriteString("\n")
	for _, p := range sortPeers(peers) {
		sb.WriteString("\n[Peer]\n")
		sb.WriteString(fmt.Sprintf("PublicKey = %s\n", p.PublicKey))
		if p.Endpoint != "" {
			sb.WriteString(fmt.Sprintf("Endpoint = %s\n", p.Endpoint))
		}
		sb.WriteString(fmt.Sprintf("AllowedIPs = %s\n", p.AllowedIP))
		sb.WriteString("PersistentKeepalive = 25\n")
	}

	return writeConfAtomic(c.path, sb.String())
}

// interfaceSection returns everything up to the first [Peer] header.
//
// A conf with no [Interface] block is refused rather than repaired: this
// process cannot reconstruct the private key, so writing a file without one
// would take the interface down on the next boot.
func interfaceSection(conf string) (string, error) {
	lines := strings.Split(conf, "\n")
	var out []string
	seenInterface := false
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "[Peer]") {
			break
		}
		if strings.HasPrefix(strings.TrimSpace(line), "[Interface]") {
			seenInterface = true
		}
		out = append(out, line)
	}
	if !seenInterface {
		return "", fmt.Errorf("no [Interface] section; refusing to rewrite")
	}
	if !strings.Contains(strings.Join(out, "\n"), "PrivateKey") {
		return "", fmt.Errorf("[Interface] has no PrivateKey; refusing to rewrite")
	}
	return strings.Join(out, "\n"), nil
}

// sortPeers orders peers by AllowedIP then public key, so an unchanged mesh
// renders identically.
func sortPeers(peers []WireGuardPeer) []WireGuardPeer {
	out := make([]WireGuardPeer, len(peers))
	copy(out, peers)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && peerLess(out[j], out[j-1]); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func peerLess(a, b WireGuardPeer) bool {
	if a.AllowedIP != b.AllowedIP {
		return a.AllowedIP < b.AllowedIP
	}
	return a.PublicKey < b.PublicKey
}

// writeConfAtomic writes content to path via a temp file in the same directory
// and a rename, at 0600 with the mode verified after the fact (bugboard #247:
// the umask is not trusted).
func writeConfAtomic(path, content string) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".wg0.conf-*")
	if err != nil {
		return fmt.Errorf("create temp conf in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp conf: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync temp conf: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp conf: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		return fmt.Errorf("chmod temp conf: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename temp conf onto %s: %w", path, err)
	}
	return forcePrivateMode(path)
}

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
		peers[pubKey] = WireGuardPeer{
			PublicKey: pubKey,
			Endpoint:  endpoint,
			AllowedIP: strings.TrimSpace(fields[3]),
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

// PersistPeers writes the peer set to wg0.conf.
func (m *WGPeerManager) PersistPeers(peers []WireGuardPeer) error {
	return m.conf.PersistPeers(peers)
}
