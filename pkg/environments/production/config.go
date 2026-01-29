package production

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/environments/templates"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
)

// ConfigGenerator manages generation of node, gateway, and service configs
type ConfigGenerator struct {
	oramaDir string
}

// NewConfigGenerator creates a new config generator
func NewConfigGenerator(oramaDir string) *ConfigGenerator {
	return &ConfigGenerator{
		oramaDir: oramaDir,
	}
}

// extractIPFromMultiaddr extracts the IP address from a peer multiaddr
// Supports IP4, IP6, DNS4, DNS6, and DNSADDR protocols
// Returns the IP address as a string, or empty string if extraction/resolution fails
func extractIPFromMultiaddr(multiaddrStr string) string {
	ma, err := multiaddr.NewMultiaddr(multiaddrStr)
	if err != nil {
		return ""
	}

	// First, try to extract direct IP address
	var ip net.IP
	var dnsName string
	multiaddr.ForEach(ma, func(c multiaddr.Component) bool {
		switch c.Protocol().Code {
		case multiaddr.P_IP4, multiaddr.P_IP6:
			ip = net.ParseIP(c.Value())
			return false // Stop iteration - found IP
		case multiaddr.P_DNS4, multiaddr.P_DNS6, multiaddr.P_DNSADDR:
			dnsName = c.Value()
			// Continue to check for IP, but remember DNS name as fallback
		}
		return true
	})

	// If we found a direct IP, return it
	if ip != nil {
		return ip.String()
	}

	// If we found a DNS name, try to resolve it
	if dnsName != "" {
		if resolvedIPs, err := net.LookupIP(dnsName); err == nil && len(resolvedIPs) > 0 {
			// Prefer IPv4 addresses, but accept IPv6 if that's all we have
			for _, resolvedIP := range resolvedIPs {
				if resolvedIP.To4() != nil {
					return resolvedIP.String()
				}
			}
			// Return first IPv6 address if no IPv4 found
			return resolvedIPs[0].String()
		}
	}

	return ""
}

// inferPeerIP extracts the IP address from peer multiaddrs
// Iterates through all peers to find a valid IP (supports DNS resolution)
// Falls back to vpsIP if provided, otherwise returns empty string
func inferPeerIP(peers []string, vpsIP string) string {
	// Try to extract IP from each peer (in order)
	for _, peer := range peers {
		if ip := extractIPFromMultiaddr(peer); ip != "" {
			return ip
		}
	}
	// Fall back to vpsIP if provided
	if vpsIP != "" {
		return vpsIP
	}
	return ""
}

// GenerateNodeConfig generates node.yaml configuration (unified architecture)
func (cg *ConfigGenerator) GenerateNodeConfig(peerAddresses []string, vpsIP string, joinAddress string, domain string, baseDomain string, enableHTTPS bool) (string, error) {
	// Generate node ID from domain or use default
	nodeID := "node"
	if domain != "" {
		// Extract node identifier from domain (e.g., "node-123" from "node-123.orama.network")
		parts := strings.Split(domain, ".")
		if len(parts) > 0 {
			nodeID = parts[0]
		}
	}

	// Determine advertise addresses - use vpsIP if provided
	// Always use port 7001 for RQLite Raft (no TLS)
	var httpAdvAddr, raftAdvAddr string
	if vpsIP != "" {
		httpAdvAddr = net.JoinHostPort(vpsIP, "5001")
		raftAdvAddr = net.JoinHostPort(vpsIP, "7001")
	} else {
		// Fallback to localhost if no vpsIP
		httpAdvAddr = "localhost:5001"
		raftAdvAddr = "localhost:7001"
	}

	// Determine RQLite join address
	// Always use port 7001 for RQLite Raft communication (no TLS)
	joinPort := "7001"

	var rqliteJoinAddr string
	if joinAddress != "" {
		// Use explicitly provided join address
		// Normalize to port 7001 (non-TLS) regardless of what was provided
		if strings.Contains(joinAddress, ":7002") {
			rqliteJoinAddr = strings.Replace(joinAddress, ":7002", ":7001", 1)
		} else {
			rqliteJoinAddr = joinAddress
		}
	} else if len(peerAddresses) > 0 {
		// Infer join address from peers
		peerIP := inferPeerIP(peerAddresses, "")
		if peerIP != "" {
			rqliteJoinAddr = net.JoinHostPort(peerIP, joinPort)
			// Validate that join address doesn't match this node's own raft address (would cause self-join)
			if rqliteJoinAddr == raftAdvAddr {
				rqliteJoinAddr = "" // Clear it - this is the first node
			}
		}
	}
	// If no join address and no peers, this is the first node - it will create the cluster

	// TLS/ACME configuration
	tlsCacheDir := ""
	httpPort := 80
	httpsPort := 443
	if enableHTTPS {
		tlsCacheDir = filepath.Join(cg.oramaDir, "tls-cache")
	}

	// Unified data directory (all nodes equal)
	// Always use port 7001 for RQLite Raft - TLS is optional and managed separately
	// The SNI gateway approach was removed to simplify certificate management
	raftInternalPort := 7001

	data := templates.NodeConfigData{
		NodeID:                 nodeID,
		P2PPort:                4001,
		DataDir:                filepath.Join(cg.oramaDir, "data"),
		RQLiteHTTPPort:         5001,
		RQLiteRaftPort:         7001,                // External SNI port
		RQLiteRaftInternalPort: raftInternalPort,    // Internal RQLite binding port
		RQLiteJoinAddress:      rqliteJoinAddr,
		BootstrapPeers:         peerAddresses,
		ClusterAPIPort:         9094,
		IPFSAPIPort:            4501,
		HTTPAdvAddress:         httpAdvAddr,
		RaftAdvAddress:         raftAdvAddr,
		UnifiedGatewayPort:     6001,
		Domain:                 domain,
		BaseDomain:             baseDomain,
		EnableHTTPS:            enableHTTPS,
		TLSCacheDir:            tlsCacheDir,
		HTTPPort:               httpPort,
		HTTPSPort:              httpsPort,
	}

	// RQLite node-to-node TLS encryption is disabled by default
	// This simplifies certificate management - RQLite uses plain TCP for internal Raft
	// HTTPS is still used for client-facing gateway traffic via autocert
	// TLS can be enabled manually later if needed for inter-node encryption

	return templates.RenderNodeConfig(data)
}

// GenerateGatewayConfig generates gateway.yaml configuration
func (cg *ConfigGenerator) GenerateGatewayConfig(peerAddresses []string, enableHTTPS bool, domain string, olricServers []string) (string, error) {
	tlsCacheDir := ""
	if enableHTTPS {
		tlsCacheDir = filepath.Join(cg.oramaDir, "tls-cache")
	}

	data := templates.GatewayConfigData{
		ListenPort:     6001,
		BootstrapPeers: peerAddresses,
		OlricServers:   olricServers,
		ClusterAPIPort: 9094,
		IPFSAPIPort:    4501,
		EnableHTTPS:    enableHTTPS,
		DomainName:     domain,
		TLSCacheDir:    tlsCacheDir,
		RQLiteDSN:      "", // Empty for now, can be configured later
	}
	return templates.RenderGatewayConfig(data)
}

// GenerateOlricConfig generates Olric configuration
func (cg *ConfigGenerator) GenerateOlricConfig(serverBindAddr string, httpPort int, memberlistBindAddr string, memberlistPort int, memberlistEnv string) (string, error) {
	data := templates.OlricConfigData{
		ServerBindAddr:        serverBindAddr,
		HTTPPort:              httpPort,
		MemberlistBindAddr:    memberlistBindAddr,
		MemberlistPort:        memberlistPort,
		MemberlistEnvironment: memberlistEnv,
	}
	return templates.RenderOlricConfig(data)
}

// SecretGenerator manages generation of shared secrets and keys
type SecretGenerator struct {
	oramaDir string
}

// NewSecretGenerator creates a new secret generator
func NewSecretGenerator(oramaDir string) *SecretGenerator {
	return &SecretGenerator{
		oramaDir: oramaDir,
	}
}

// ValidateClusterSecret ensures a cluster secret is 32 bytes of hex
func ValidateClusterSecret(secret string) error {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return fmt.Errorf("cluster secret cannot be empty")
	}
	if len(secret) != 64 {
		return fmt.Errorf("cluster secret must be 64 hex characters (32 bytes)")
	}
	if _, err := hex.DecodeString(secret); err != nil {
		return fmt.Errorf("cluster secret must be valid hex: %w", err)
	}
	return nil
}

// EnsureClusterSecret gets or generates the IPFS Cluster secret
func (sg *SecretGenerator) EnsureClusterSecret() (string, error) {
	secretPath := filepath.Join(sg.oramaDir, "secrets", "cluster-secret")
	secretDir := filepath.Dir(secretPath)

	// Ensure secrets directory exists with restricted permissions (0700)
	if err := os.MkdirAll(secretDir, 0700); err != nil {
		return "", fmt.Errorf("failed to create secrets directory: %w", err)
	}
	// Ensure directory permissions are correct even if it already existed
	if err := os.Chmod(secretDir, 0700); err != nil {
		return "", fmt.Errorf("failed to set secrets directory permissions: %w", err)
	}

	// Try to read existing secret
	if data, err := os.ReadFile(secretPath); err == nil {
		secret := strings.TrimSpace(string(data))
		if len(secret) == 64 {
			if err := ensureSecretFilePermissions(secretPath); err != nil {
				return "", err
			}
			return secret, nil
		}
	}

	// Generate new secret (32 bytes = 64 hex chars)
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("failed to generate cluster secret: %w", err)
	}
	secret := hex.EncodeToString(bytes)

	// Write and protect
	if err := os.WriteFile(secretPath, []byte(secret), 0600); err != nil {
		return "", fmt.Errorf("failed to save cluster secret: %w", err)
	}
	if err := ensureSecretFilePermissions(secretPath); err != nil {
		return "", err
	}

	return secret, nil
}

func ensureSecretFilePermissions(secretPath string) error {
	if err := os.Chmod(secretPath, 0600); err != nil {
		return fmt.Errorf("failed to set permissions on %s: %w", secretPath, err)
	}

	if usr, err := user.Lookup("debros"); err == nil {
		uid, err := strconv.Atoi(usr.Uid)
		if err != nil {
			return fmt.Errorf("failed to parse debros UID: %w", err)
		}
		gid, err := strconv.Atoi(usr.Gid)
		if err != nil {
			return fmt.Errorf("failed to parse debros GID: %w", err)
		}
		if err := os.Chown(secretPath, uid, gid); err != nil {
			return fmt.Errorf("failed to change ownership of %s: %w", secretPath, err)
		}
	}

	return nil
}

// EnsureSwarmKey gets or generates the IPFS private swarm key
func (sg *SecretGenerator) EnsureSwarmKey() ([]byte, error) {
	swarmKeyPath := filepath.Join(sg.oramaDir, "secrets", "swarm.key")
	secretDir := filepath.Dir(swarmKeyPath)

	// Ensure secrets directory exists with restricted permissions (0700)
	if err := os.MkdirAll(secretDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create secrets directory: %w", err)
	}
	// Ensure directory permissions are correct even if it already existed
	if err := os.Chmod(secretDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to set secrets directory permissions: %w", err)
	}

	// Try to read existing key — validate and auto-fix if corrupted (e.g. double headers)
	if data, err := os.ReadFile(swarmKeyPath); err == nil {
		content := string(data)
		if strings.Contains(content, "/key/swarm/psk/1.0.0/") {
			// Extract hex and rebuild clean file
			lines := strings.Split(strings.TrimSpace(content), "\n")
			hexKey := ""
			for i := len(lines) - 1; i >= 0; i-- {
				line := strings.TrimSpace(lines[i])
				if line != "" && !strings.HasPrefix(line, "/") {
					hexKey = line
					break
				}
			}
			clean := fmt.Sprintf("/key/swarm/psk/1.0.0/\n/base16/\n%s\n", hexKey)
			if clean != content {
				_ = os.WriteFile(swarmKeyPath, []byte(clean), 0600)
			}
			return []byte(clean), nil
		}
	}

	// Generate new key (32 bytes)
	keyBytes := make([]byte, 32)
	if _, err := rand.Read(keyBytes); err != nil {
		return nil, fmt.Errorf("failed to generate swarm key: %w", err)
	}

	keyHex := strings.ToUpper(hex.EncodeToString(keyBytes))
	content := fmt.Sprintf("/key/swarm/psk/1.0.0/\n/base16/\n%s\n", keyHex)

	// Write and protect
	if err := os.WriteFile(swarmKeyPath, []byte(content), 0600); err != nil {
		return nil, fmt.Errorf("failed to save swarm key: %w", err)
	}

	return []byte(content), nil
}

// EnsureNodeIdentity gets or generates the node's LibP2P identity (unified - no bootstrap/node distinction)
func (sg *SecretGenerator) EnsureNodeIdentity() (peer.ID, error) {
	// Unified data directory (no bootstrap/node distinction)
	keyDir := filepath.Join(sg.oramaDir, "data")
	keyPath := filepath.Join(keyDir, "identity.key")

	// Ensure data directory exists
	if err := os.MkdirAll(keyDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create data directory: %w", err)
	}

	// Try to read existing key
	if data, err := os.ReadFile(keyPath); err == nil {
		priv, err := crypto.UnmarshalPrivateKey(data)
		if err == nil {
			pub := priv.GetPublic()
			peerID, _ := peer.IDFromPublicKey(pub)
			return peerID, nil
		}
	}

	// Generate new identity
	priv, pub, err := crypto.GenerateKeyPair(crypto.Ed25519, 2048)
	if err != nil {
		return "", fmt.Errorf("failed to generate identity: %w", err)
	}

	peerID, _ := peer.IDFromPublicKey(pub)

	// Marshal and save private key
	keyData, err := crypto.MarshalPrivateKey(priv)
	if err != nil {
		return "", fmt.Errorf("failed to marshal private key: %w", err)
	}

	if err := os.WriteFile(keyPath, keyData, 0600); err != nil {
		return "", fmt.Errorf("failed to save identity key: %w", err)
	}

	return peerID, nil
}

// SaveConfig writes a configuration file to disk
func (sg *SecretGenerator) SaveConfig(filename string, content string) error {
	var configDir string
	// gateway.yaml goes to data/ directory, other configs go to configs/
	if filename == "gateway.yaml" {
		configDir = filepath.Join(sg.oramaDir, "data")
	} else {
		configDir = filepath.Join(sg.oramaDir, "configs")
	}

	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	configPath := filepath.Join(configDir, filename)
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write config %s: %w", filename, err)
	}

	// Fix ownership
	exec.Command("chown", "debros:debros", configPath).Run()

	return nil
}
