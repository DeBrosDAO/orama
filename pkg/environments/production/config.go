package production

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/environments/templates"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
)

// ConfigGenerator manages generation of node, gateway, and service configs
type ConfigGenerator struct {
	debrosDir string
}

// NewConfigGenerator creates a new config generator
func NewConfigGenerator(debrosDir string) *ConfigGenerator {
	return &ConfigGenerator{
		debrosDir: debrosDir,
	}
}

// extractIPFromMultiaddr extracts the IP address from a bootstrap peer multiaddr
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

// inferBootstrapIP extracts the IP address from bootstrap peer multiaddrs
// Iterates through all bootstrap peers to find a valid IP (supports DNS resolution)
// Falls back to vpsIP if provided, otherwise returns empty string
func inferBootstrapIP(bootstrapPeers []string, vpsIP string) string {
	// Try to extract IP from each bootstrap peer (in order)
	for _, peer := range bootstrapPeers {
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

// GenerateNodeConfig generates node.yaml configuration
func (cg *ConfigGenerator) GenerateNodeConfig(isBootstrap bool, bootstrapPeers []string, vpsIP string, bootstrapJoin string) (string, error) {
	var nodeID string
	if isBootstrap {
		nodeID = "bootstrap"
	} else {
		nodeID = "node"
	}

	// Determine advertise addresses
	// For bootstrap: use vpsIP if provided, otherwise localhost
	// For regular nodes: infer from bootstrap peers or use vpsIP
	var httpAdvAddr, raftAdvAddr string
	if isBootstrap {
		if vpsIP != "" {
			httpAdvAddr = net.JoinHostPort(vpsIP, "5001")
			raftAdvAddr = net.JoinHostPort(vpsIP, "7001")
		} else {
			httpAdvAddr = "localhost:5001"
			raftAdvAddr = "localhost:7001"
		}
	} else {
		// Regular node: infer from bootstrap peers or use vpsIP
		bootstrapIP := inferBootstrapIP(bootstrapPeers, vpsIP)
		if bootstrapIP != "" {
			// Use the bootstrap IP for advertise addresses (this node should be reachable at same network)
			// If vpsIP is provided, use it; otherwise use bootstrap IP
			if vpsIP != "" {
				httpAdvAddr = net.JoinHostPort(vpsIP, "5001")
				raftAdvAddr = net.JoinHostPort(vpsIP, "7001")
			} else {
				httpAdvAddr = net.JoinHostPort(bootstrapIP, "5001")
				raftAdvAddr = net.JoinHostPort(bootstrapIP, "7001")
			}
		} else {
			// Fallback to localhost if nothing can be inferred
			httpAdvAddr = "localhost:5001"
			raftAdvAddr = "localhost:7001"
		}
	}

	if isBootstrap {
		// Bootstrap node - populate peer list and optional join address
		data := templates.BootstrapConfigData{
			NodeID:            nodeID,
			P2PPort:           4001,
			DataDir:           filepath.Join(cg.debrosDir, "data", "bootstrap"),
			RQLiteHTTPPort:    5001,
			RQLiteRaftPort:    7001,
			ClusterAPIPort:    9094,
			IPFSAPIPort:       4501,
			BootstrapPeers:    bootstrapPeers,
			RQLiteJoinAddress: bootstrapJoin,
			HTTPAdvAddress:    httpAdvAddr,
			RaftAdvAddress:    raftAdvAddr,
		}
		return templates.RenderBootstrapConfig(data)
	}

	// Regular node - infer join address from bootstrap peers
	// MUST extract from bootstrap_peers - no fallback to vpsIP (would cause self-join)
	var rqliteJoinAddr string
	bootstrapIP := inferBootstrapIP(bootstrapPeers, "")
	if bootstrapIP == "" {
		// Try to extract from first bootstrap peer directly as fallback
		if len(bootstrapPeers) > 0 {
			if extractedIP := extractIPFromMultiaddr(bootstrapPeers[0]); extractedIP != "" {
				bootstrapIP = extractedIP
			}
		}

		// If still no IP, fail - we cannot join without a valid bootstrap address
		if bootstrapIP == "" {
			return "", fmt.Errorf("cannot determine RQLite join address: failed to extract IP from bootstrap peers %v (required for non-bootstrap nodes)", bootstrapPeers)
		}
	}

	rqliteJoinAddr = net.JoinHostPort(bootstrapIP, "7001")

	// Validate that join address doesn't match this node's own raft address (would cause self-join)
	if rqliteJoinAddr == raftAdvAddr {
		return "", fmt.Errorf("invalid configuration: rqlite_join_address (%s) cannot match raft_adv_address (%s) - node cannot join itself", rqliteJoinAddr, raftAdvAddr)
	}

	data := templates.NodeConfigData{
		NodeID:            nodeID,
		P2PPort:           4001,
		DataDir:           filepath.Join(cg.debrosDir, "data", "node"),
		RQLiteHTTPPort:    5001,
		RQLiteRaftPort:    7001,
		RQLiteJoinAddress: rqliteJoinAddr,
		BootstrapPeers:    bootstrapPeers,
		ClusterAPIPort:    9094,
		IPFSAPIPort:       4501,
		HTTPAdvAddress:    httpAdvAddr,
		RaftAdvAddress:    raftAdvAddr,
	}
	return templates.RenderNodeConfig(data)
}

// GenerateGatewayConfig generates gateway.yaml configuration
func (cg *ConfigGenerator) GenerateGatewayConfig(bootstrapPeers []string, enableHTTPS bool, domain string, olricServers []string) (string, error) {
	tlsCacheDir := ""
	if enableHTTPS {
		tlsCacheDir = filepath.Join(cg.debrosDir, "tls-cache")
	}

	data := templates.GatewayConfigData{
		ListenPort:     6001,
		BootstrapPeers: bootstrapPeers,
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
func (cg *ConfigGenerator) GenerateOlricConfig(bindAddr string, httpPort, memberlistPort int) (string, error) {
	data := templates.OlricConfigData{
		BindAddr:       bindAddr,
		HTTPPort:       httpPort,
		MemberlistPort: memberlistPort,
	}
	return templates.RenderOlricConfig(data)
}

// SecretGenerator manages generation of shared secrets and keys
type SecretGenerator struct {
	debrosDir string
}

// NewSecretGenerator creates a new secret generator
func NewSecretGenerator(debrosDir string) *SecretGenerator {
	return &SecretGenerator{
		debrosDir: debrosDir,
	}
}

// EnsureClusterSecret gets or generates the IPFS Cluster secret
func (sg *SecretGenerator) EnsureClusterSecret() (string, error) {
	secretPath := filepath.Join(sg.debrosDir, "secrets", "cluster-secret")
	secretDir := filepath.Dir(secretPath)

	// Ensure secrets directory exists
	if err := os.MkdirAll(secretDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create secrets directory: %w", err)
	}

	// Try to read existing secret
	if data, err := os.ReadFile(secretPath); err == nil {
		secret := strings.TrimSpace(string(data))
		if len(secret) == 64 {
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

	return secret, nil
}

// EnsureSwarmKey gets or generates the IPFS private swarm key
func (sg *SecretGenerator) EnsureSwarmKey() ([]byte, error) {
	swarmKeyPath := filepath.Join(sg.debrosDir, "secrets", "swarm.key")
	secretDir := filepath.Dir(swarmKeyPath)

	// Ensure secrets directory exists
	if err := os.MkdirAll(secretDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create secrets directory: %w", err)
	}

	// Try to read existing key
	if data, err := os.ReadFile(swarmKeyPath); err == nil {
		if strings.Contains(string(data), "/key/swarm/psk/1.0.0/") {
			return data, nil
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

// EnsureNodeIdentity gets or generates the node's LibP2P identity
func (sg *SecretGenerator) EnsureNodeIdentity(nodeType string) (peer.ID, error) {
	keyDir := filepath.Join(sg.debrosDir, "data", nodeType)
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
		configDir = filepath.Join(sg.debrosDir, "data")
	} else {
		configDir = filepath.Join(sg.debrosDir, "configs")
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
