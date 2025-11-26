package development

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/encryption"
	"github.com/DeBrosOfficial/network/pkg/environments/templates"
)

// ConfigEnsurer handles all config file creation and validation
type ConfigEnsurer struct {
	oramaDir string
}

// NewConfigEnsurer creates a new config ensurer
func NewConfigEnsurer(oramaDir string) *ConfigEnsurer {
	return &ConfigEnsurer{
		oramaDir: oramaDir,
	}
}

// EnsureAll ensures all necessary config files and secrets exist
func (ce *ConfigEnsurer) EnsureAll() error {
	// Create directories
	if err := os.MkdirAll(ce.oramaDir, 0755); err != nil {
		return fmt.Errorf("failed to create .orama directory: %w", err)
	}

	if err := os.MkdirAll(filepath.Join(ce.oramaDir, "logs"), 0755); err != nil {
		return fmt.Errorf("failed to create logs directory: %w", err)
	}

	// Ensure shared secrets
	if err := ce.ensureSharedSecrets(); err != nil {
		return fmt.Errorf("failed to ensure shared secrets: %w", err)
	}

	// Load topology
	topology := DefaultTopology()

	// Generate identities for first two nodes and collect their multiaddrs as peer addresses
	// All nodes use these addresses for initial peer discovery
	peerAddrs := []string{}
	for i := 0; i < 2 && i < len(topology.Nodes); i++ {
		nodeSpec := topology.Nodes[i]
		addr, err := ce.ensureNodeIdentity(nodeSpec)
		if err != nil {
			return fmt.Errorf("failed to ensure identity for %s: %w", nodeSpec.Name, err)
		}
		peerAddrs = append(peerAddrs, addr)
	}

	// Ensure configs for all nodes
	for _, nodeSpec := range topology.Nodes {
		if err := ce.ensureNodeConfig(nodeSpec, peerAddrs); err != nil {
			return fmt.Errorf("failed to ensure config for %s: %w", nodeSpec.Name, err)
		}
	}

	// Ensure gateway config
	if err := ce.ensureGateway(peerAddrs); err != nil {
		return fmt.Errorf("failed to ensure gateway: %w", err)
	}

	// Ensure Olric config
	if err := ce.ensureOlric(); err != nil {
		return fmt.Errorf("failed to ensure olric: %w", err)
	}

	return nil
}

// ensureSharedSecrets creates cluster secret and swarm key if they don't exist
func (ce *ConfigEnsurer) ensureSharedSecrets() error {
	secretPath := filepath.Join(ce.oramaDir, "cluster-secret")
	if _, err := os.Stat(secretPath); os.IsNotExist(err) {
		secret := generateRandomHex(64) // 64 hex chars = 32 bytes
		if err := os.WriteFile(secretPath, []byte(secret), 0600); err != nil {
			return fmt.Errorf("failed to write cluster secret: %w", err)
		}
		fmt.Printf("✓ Generated cluster secret\n")
	}

	swarmKeyPath := filepath.Join(ce.oramaDir, "swarm.key")
	if _, err := os.Stat(swarmKeyPath); os.IsNotExist(err) {
		keyHex := strings.ToUpper(generateRandomHex(64))
		content := fmt.Sprintf("/key/swarm/psk/1.0.0/\n/base16/\n%s\n", keyHex)
		if err := os.WriteFile(swarmKeyPath, []byte(content), 0600); err != nil {
			return fmt.Errorf("failed to write swarm key: %w", err)
		}
		fmt.Printf("✓ Generated IPFS swarm key\n")
	}

	return nil
}

// ensureNodeIdentity creates or loads a node identity and returns its multiaddr
func (ce *ConfigEnsurer) ensureNodeIdentity(nodeSpec NodeSpec) (string, error) {
	nodeDir := filepath.Join(ce.oramaDir, nodeSpec.DataDir)
	identityPath := filepath.Join(nodeDir, "identity.key")

	// Create identity if missing
	var peerID string
	if _, err := os.Stat(identityPath); os.IsNotExist(err) {
		if err := os.MkdirAll(nodeDir, 0755); err != nil {
			return "", fmt.Errorf("failed to create node directory: %w", err)
		}

		info, err := encryption.GenerateIdentity()
		if err != nil {
			return "", fmt.Errorf("failed to generate identity: %w", err)
		}

		if err := encryption.SaveIdentity(info, identityPath); err != nil {
			return "", fmt.Errorf("failed to save identity: %w", err)
		}

		peerID = info.PeerID.String()
		fmt.Printf("✓ Generated %s identity (Peer ID: %s)\n", nodeSpec.Name, peerID)
	} else {
		info, err := encryption.LoadIdentity(identityPath)
		if err != nil {
			return "", fmt.Errorf("failed to load identity: %w", err)
		}
		peerID = info.PeerID.String()
	}

	// Return multiaddr
	return fmt.Sprintf("/ip4/127.0.0.1/tcp/%d/p2p/%s", nodeSpec.P2PPort, peerID), nil
}

// ensureNodeConfig creates or updates a node configuration
func (ce *ConfigEnsurer) ensureNodeConfig(nodeSpec NodeSpec, peerAddrs []string) error {
	nodeDir := filepath.Join(ce.oramaDir, nodeSpec.DataDir)
	configPath := filepath.Join(ce.oramaDir, nodeSpec.ConfigFilename)

	if err := os.MkdirAll(nodeDir, 0755); err != nil {
		return fmt.Errorf("failed to create node directory: %w", err)
	}

	// Generate node config (all nodes are unified)
	data := templates.NodeConfigData{
		NodeID:             nodeSpec.Name,
		P2PPort:            nodeSpec.P2PPort,
		DataDir:            nodeDir,
		RQLiteHTTPPort:     nodeSpec.RQLiteHTTPPort,
		RQLiteRaftPort:     nodeSpec.RQLiteRaftPort,
		RQLiteJoinAddress:  nodeSpec.RQLiteJoinTarget,
		BootstrapPeers:     peerAddrs,
		ClusterAPIPort:     nodeSpec.ClusterAPIPort,
		IPFSAPIPort:        nodeSpec.IPFSAPIPort,
		UnifiedGatewayPort: nodeSpec.UnifiedGatewayPort,
	}

	config, err := templates.RenderNodeConfig(data)
	if err != nil {
		return fmt.Errorf("failed to render node config: %w", err)
	}

	if err := os.WriteFile(configPath, []byte(config), 0644); err != nil {
		return fmt.Errorf("failed to write node config: %w", err)
	}

	fmt.Printf("✓ Generated %s.yaml\n", nodeSpec.Name)

	return nil
}

// ensureGateway creates gateway config
func (ce *ConfigEnsurer) ensureGateway(peerAddrs []string) error {
	configPath := filepath.Join(ce.oramaDir, "gateway.yaml")

	// Get first node's cluster API port for default
	topology := DefaultTopology()
	firstNode := topology.GetFirstNode()

	data := templates.GatewayConfigData{
		ListenPort:     topology.GatewayPort,
		BootstrapPeers: peerAddrs,
		OlricServers:   []string{fmt.Sprintf("127.0.0.1:%d", topology.OlricHTTPPort)},
		ClusterAPIPort: firstNode.ClusterAPIPort,
		IPFSAPIPort:    firstNode.IPFSAPIPort,
	}

	config, err := templates.RenderGatewayConfig(data)
	if err != nil {
		return fmt.Errorf("failed to render gateway config: %w", err)
	}

	if err := os.WriteFile(configPath, []byte(config), 0644); err != nil {
		return fmt.Errorf("failed to write gateway config: %w", err)
	}

	fmt.Printf("✓ Generated gateway.yaml\n")
	return nil
}

// ensureOlric creates Olric config
func (ce *ConfigEnsurer) ensureOlric() error {
	configPath := filepath.Join(ce.oramaDir, "olric-config.yaml")

	topology := DefaultTopology()
	data := templates.OlricConfigData{
		BindAddr:       "127.0.0.1",
		HTTPPort:       topology.OlricHTTPPort,
		MemberlistPort: topology.OlricMemberPort,
	}

	config, err := templates.RenderOlricConfig(data)
	if err != nil {
		return fmt.Errorf("failed to render olric config: %w", err)
	}

	if err := os.WriteFile(configPath, []byte(config), 0644); err != nil {
		return fmt.Errorf("failed to write olric config: %w", err)
	}

	fmt.Printf("✓ Generated olric-config.yaml\n")
	return nil
}

// generateRandomHex generates a random hex string of specified length
func generateRandomHex(length int) string {
	bytes := make([]byte, length/2)
	if _, err := rand.Read(bytes); err != nil {
		panic(fmt.Sprintf("failed to generate random bytes: %v", err))
	}
	return hex.EncodeToString(bytes)
}
