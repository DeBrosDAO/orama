package privhelper

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/wireguard"
)

// Command returns a command that runs tool with args as root.
//
// The installer is root and runs systemctl and ufw directly. Everything else
// runs as the orama user under sandboxing that sets no_new_privs — systemd
// implies it for an unprivileged unit with PrivateDevices=, ProtectKernel*= or
// RestrictNamespaces= — so sudo cannot work there at all. Those callers run
// `orama-privhelper call`, which hands the request to the root helper over
// its socket; no privilege is gained in the calling process.
func Command(tool string, args ...string) *exec.Cmd {
	if os.Geteuid() == 0 {
		if tool == ToolSystemctl || tool == ToolUFW {
			return exec.Command(tool, args...)
		}
		return exec.Command(Path, append([]string{"run", tool}, args...)...)
	}
	return exec.Command(Path, append([]string{"call", tool}, args...)...)
}

// PersistWireGuardPeers rewrites wg0.conf's [Peer] sections to peers through
// the helper.
func PersistWireGuardPeers(peers []wireguard.Peer) error {
	if peers == nil {
		peers = []wireguard.Peer{}
	}
	payload, err := json.Marshal(peers)
	if err != nil {
		return fmt.Errorf("encode peers: %w", err)
	}
	cmd := Command(ToolWireGuard, wgPersistPeers)
	cmd.Stdin = strings.NewReader(string(payload))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("persist %d WireGuard peers: %w: %s", len(peers), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// AddWireGuardPeer applies p to wg0 and persists it, through the helper.
func AddWireGuardPeer(p wireguard.Peer) error {
	cmd := Command(ToolWireGuard, wgAddPeer, p.PublicKey, p.Endpoint, p.AllowedIP)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("add WireGuard peer %s: %w: %s", p.AllowedIP, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// RemoveWireGuardPeer removes the peer holding allowedIP (a /32) from wg0 and
// from wg0.conf, through the helper. It matches the address exactly.
func RemoveWireGuardPeer(allowedIP string) error {
	cmd := Command(ToolWireGuard, wgRemovePeer, allowedIP)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("remove WireGuard peer %s: %w: %s", allowedIP, err, strings.TrimSpace(string(out)))
	}
	return nil
}
