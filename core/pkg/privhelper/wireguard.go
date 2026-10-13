package privhelper

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/DeBrosOfficial/network/pkg/constants"
	"net"
	"strconv"

	"github.com/DeBrosOfficial/network/pkg/wireguard"
)

// ToolWireGuard is the helper's own WireGuard operations. /etc/wireguard is
// root's and must stay so: wg-quick runs the conf's PostUp/PreUp lines as
// root, so a conf the orama user could write would be root code execution.
// The helper takes peers as data, validates each field, and rewrites only the
// [Peer] sections — nothing the caller sends reaches the [Interface] block.
const ToolWireGuard = "wireguard"

// WireGuard operations.
const (
	// wgAddPeer <public-key> <endpoint> <allowed-ip>: apply to wg0 and persist.
	wgAddPeer = "add-peer"
	// wgPersistPeers: rewrite the [Peer] sections to the JSON peer list on
	// the request's input.
	wgPersistPeers = "persist-peers"
	// wgRemovePeer <allowed-ip>: remove the peer holding that /32 from wg0 and
	// from the conf.
	wgRemovePeer = "remove-peer"
)

// maxPersistedPeers bounds a persist-peers request; the mesh is one peer per
// node.
const maxPersistedPeers = 4096

// overlayNet is the WireGuard mesh every AllowedIP must lie in.
var overlayNet = mustCIDR(constants.WireGuardSubnet)

func mustCIDR(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return n
}

func validateWireGuard(args []string) error {
	switch {
	case len(args) == 4 && args[0] == wgAddPeer:
		return ValidatePeer(wireguard.Peer{PublicKey: args[1], Endpoint: args[2], AllowedIP: args[3]})
	case len(args) == 1 && args[0] == wgPersistPeers:
		return nil // the peers arrive as input; see ParsePersistInput
	case len(args) == 2 && args[0] == wgRemovePeer:
		return validateOverlayHost(args[1])
	default:
		return fmt.Errorf("wireguard %q is not allowed", args)
	}
}

// NeedsInput reports whether an invocation reads a payload.
func (inv Invocation) NeedsInput() bool {
	switch inv.Tool {
	case ToolWireGuard:
		return len(inv.Args) == 1 && inv.Args[0] == wgPersistPeers
	case ToolDeploy:
		return inv.Args[0] == deploySetEnv || inv.Args[0] == deploySetToken
	case ToolUnitEnv:
		return inv.Args[0] == unitEnvSet
	case ToolGatewayKey:
		// put carries the PEM. Forgetting this stores a zero-byte key: call
		// and run only read stdin when NeedsInput is set.
		return len(inv.Args) > 0 && inv.Args[0] == gatewayKeyPut
	default:
		return false
	}
}

// ValidatePeer checks a peer field by field: a 32-byte base64 public key, an
// IP:port endpoint (or none), and a single overlay address as /32.
func ValidatePeer(p wireguard.Peer) error {
	key, err := base64.StdEncoding.DecodeString(p.PublicKey)
	// Canonical form only: the decoder skips '\r' and '\n', so a key with a
	// newline in it decoded to 32 bytes and landed in wg0.conf as two lines.
	if err != nil || len(key) != 32 || base64.StdEncoding.EncodeToString(key) != p.PublicKey {
		return fmt.Errorf("public key %q is not a base64 32-byte WireGuard key", p.PublicKey)
	}
	if p.Endpoint != "" {
		host, port, err := net.SplitHostPort(p.Endpoint)
		if err != nil || net.ParseIP(host) == nil {
			return fmt.Errorf("endpoint %q is not ip:port", p.Endpoint)
		}
		if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("endpoint %q has no valid port", p.Endpoint)
		}
	}
	return validateOverlayHost(p.AllowedIP)
}

// validateOverlayHost accepts one mesh address as a /32.
func validateOverlayHost(allowedIP string) error {
	ip, ipnet, err := net.ParseCIDR(allowedIP)
	if err != nil || ip.To4() == nil || allowedIP != ipnet.String() {
		return fmt.Errorf("allowed IP %q is not a canonical IPv4 CIDR", allowedIP)
	}
	if ones, bits := ipnet.Mask.Size(); ones != 32 || bits != 32 {
		return fmt.Errorf("allowed IP %q must be a single address (/32)", allowedIP)
	}
	if !overlayNet.Contains(ip) {
		return fmt.Errorf("allowed IP %q is outside the mesh %s", allowedIP, overlayNet)
	}
	return nil
}

// ParsePersistInput decodes and validates a persist-peers payload.
func ParsePersistInput(input []byte) ([]wireguard.Peer, error) {
	var peers []wireguard.Peer
	if err := json.Unmarshal(input, &peers); err != nil {
		return nil, fmt.Errorf("peer list is not JSON: %w", err)
	}
	if len(peers) > maxPersistedPeers {
		return nil, fmt.Errorf("%d peers is more than %d", len(peers), maxPersistedPeers)
	}
	seenKeys := make(map[string]bool, len(peers))
	seenIPs := make(map[string]bool, len(peers))
	for i, p := range peers {
		if err := ValidatePeer(p); err != nil {
			return nil, fmt.Errorf("peer %d: %w", i, err)
		}
		if seenKeys[p.PublicKey] {
			return nil, fmt.Errorf("peer %d: public key listed twice", i)
		}
		// wg routes an address to one peer only; a second peer claiming the
		// same /32 silently takes it from the first when wg-quick applies the
		// conf, cutting that node off the mesh.
		if seenIPs[p.AllowedIP] {
			return nil, fmt.Errorf("peer %d: allowed IP %s is already another peer's", i, p.AllowedIP)
		}
		seenKeys[p.PublicKey] = true
		seenIPs[p.AllowedIP] = true
	}
	return peers, nil
}
