package wireguard

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Peer is one [Peer] of the mesh: a node's public key, where to reach it, and
// the overlay address it owns.
type Peer struct {
	PublicKey string `json:"public_key"` // Base64-encoded public key
	Endpoint  string `json:"endpoint"`   // e.g., "141.227.165.154:51820"
	AllowedIP string `json:"allowed_ip"` // e.g., "10.0.0.2/32"
}

// DefaultConfPath is the interface config wg-quick reads at boot.
const DefaultConfPath = "/etc/wireguard/wg0.conf"

// Conf owns /etc/wireguard/wg0.conf for the running node.
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
type Conf struct {
	path string
}

// NewConf returns a conf owner for path. An empty path means the default.
func NewConf(path string) *Conf {
	if path == "" {
		path = DefaultConfPath
	}
	return &Conf{path: path}
}

// Path is the file this owner writes.
func (c *Conf) Path() string { return c.path }

// PersistPeers rewrites the [Peer] sections to exactly peers, leaving the
// [Interface] block untouched.
//
// Peers are written in sorted AllowedIP order so an unchanged mesh produces a
// byte-identical file and a diff shows only real membership changes.
func (c *Conf) PersistPeers(peers []Peer) error {
	return c.locked(func() error { return c.persistPeersLocked(peers) })
}

// persistPeersLocked is PersistPeers with the conf lock held.
func (c *Conf) persistPeersLocked(peers []Peer) error {
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
func sortPeers(peers []Peer) []Peer {
	out := make([]Peer, len(peers))
	copy(out, peers)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && peerLess(out[j], out[j-1]); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func peerLess(a, b Peer) bool {
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
	return privateMode(path)
}

// Peers parses the [Peer] sections of the conf.
func (c *Conf) Peers() ([]Peer, error) {
	data, err := os.ReadFile(c.path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", c.path, err)
	}
	return parsePeerSections(string(data)), nil
}

// AddPeer adds p to the persisted peer set, replacing any peer with the same
// public key or the same address (a node replaced on the same overlay IP), and
// leaves every other peer and the [Interface] block as they are.
func (c *Conf) AddPeer(p Peer) error {
	return c.locked(func() error {
		peers, err := c.Peers()
		if err != nil {
			return err
		}
		out := make([]Peer, 0, len(peers)+1)
		for _, existing := range peers {
			if existing.PublicKey != p.PublicKey && existing.AllowedIP != p.AllowedIP {
				out = append(out, existing)
			}
		}
		return c.persistPeersLocked(append(out, p))
	})
}

// RemovePeersByAllowedIP drops every peer holding exactly allowedIP and
// returns how many it removed.
func (c *Conf) RemovePeersByAllowedIP(allowedIP string) (int, error) {
	removed := 0
	err := c.locked(func() error {
		peers, err := c.Peers()
		if err != nil {
			return err
		}
		out := make([]Peer, 0, len(peers))
		for _, existing := range peers {
			if existing.AllowedIP == allowedIP {
				removed++
				continue
			}
			out = append(out, existing)
		}
		if removed == 0 {
			return nil
		}
		return c.persistPeersLocked(out)
	})
	return removed, err
}

// locked runs fn holding an exclusive lock on the conf. Every change is a
// read-modify-write, and the privileged helper serves requests in parallel
// (one instance per connection): without the lock a join's add-peer and the
// periodic persist could each read, modify and rename, and one would lose
// the other's peer.
func (c *Conf) locked(fn func() error) error {
	lockPath := filepath.Join(filepath.Dir(c.path), ".wg0.conf.lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open conf lock %s: %w", lockPath, err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock %s: %w", lockPath, err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}

// parsePeerSections reads PublicKey, Endpoint and AllowedIPs from each [Peer].
func parsePeerSections(conf string) []Peer {
	var peers []Peer
	var cur *Peer
	flush := func() {
		if cur != nil && cur.PublicKey != "" {
			peers = append(peers, *cur)
		}
		cur = nil
	}
	for _, raw := range strings.Split(conf, "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "[") {
			flush()
			if line == "[Peer]" {
				cur = &Peer{}
			}
			continue
		}
		if cur == nil {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		switch key {
		case "PublicKey":
			cur.PublicKey = value
		case "Endpoint":
			cur.Endpoint = value
		case "AllowedIPs":
			cur.AllowedIP = value
		}
	}
	flush()
	return peers
}

// privateMode chmods path 0600 and verifies the result (bugboard #247: the
// umask is not trusted).
func privateMode(path string) error {
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("chmod 0600 %s: %w", path, err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	}
	if fi.Mode().Perm() != 0o600 {
		return fmt.Errorf("%s mode %o, want 0600", path, fi.Mode().Perm())
	}
	return nil
}
