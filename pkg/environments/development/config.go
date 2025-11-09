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
	debrosDir string
}

// NewConfigEnsurer creates a new config ensurer
func NewConfigEnsurer(debrosDir string) *ConfigEnsurer {
	return &ConfigEnsurer{
		debrosDir: debrosDir,
	}
}

// EnsureAll ensures all necessary config files and secrets exist
func (ce *ConfigEnsurer) EnsureAll() error {
	// Create directories
	if err := os.MkdirAll(ce.debrosDir, 0755); err != nil {
		return fmt.Errorf("failed to create .debros directory: %w", err)
	}

	if err := os.MkdirAll(filepath.Join(ce.debrosDir, "logs"), 0755); err != nil {
		return fmt.Errorf("failed to create logs directory: %w", err)
	}

	// Ensure shared secrets
	if err := ce.ensureSharedSecrets(); err != nil {
		return fmt.Errorf("failed to ensure shared secrets: %w", err)
	}

	// Ensure bootstrap config and identity
	if err := ce.ensureBootstrap(); err != nil {
		return fmt.Errorf("failed to ensure bootstrap: %w", err)
	}

	// Ensure node2 and node3 configs
	if err := ce.ensureNode2And3(); err != nil {
		return fmt.Errorf("failed to ensure nodes: %w", err)
	}

	// Ensure gateway config
	if err := ce.ensureGateway(); err != nil {
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
	secretPath := filepath.Join(ce.debrosDir, "cluster-secret")
	if _, err := os.Stat(secretPath); os.IsNotExist(err) {
		secret := generateRandomHex(64) // 64 hex chars = 32 bytes
		if err := os.WriteFile(secretPath, []byte(secret), 0600); err != nil {
			return fmt.Errorf("failed to write cluster secret: %w", err)
		}
		fmt.Printf("✓ Generated cluster secret\n")
	}

	swarmKeyPath := filepath.Join(ce.debrosDir, "swarm.key")
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

// ensureBootstrap creates bootstrap identity and config
func (ce *ConfigEnsurer) ensureBootstrap() error {
	bootstrapDir := filepath.Join(ce.debrosDir, "bootstrap")
	identityPath := filepath.Join(bootstrapDir, "identity.key")

	// Create identity if missing
	var bootstrapPeerID string
	if _, err := os.Stat(identityPath); os.IsNotExist(err) {
		if err := os.MkdirAll(bootstrapDir, 0755); err != nil {
			return fmt.Errorf("failed to create bootstrap directory: %w", err)
		}

		info, err := encryption.GenerateIdentity()
		if err != nil {
			return fmt.Errorf("failed to generate bootstrap identity: %w", err)
		}

		if err := encryption.SaveIdentity(info, identityPath); err != nil {
			return fmt.Errorf("failed to save bootstrap identity: %w", err)
		}

		bootstrapPeerID = info.PeerID.String()
		fmt.Printf("✓ Generated bootstrap identity (Peer ID: %s)\n", bootstrapPeerID)
	} else {
		info, err := encryption.LoadIdentity(identityPath)
		if err != nil {
			return fmt.Errorf("failed to load bootstrap identity: %w", err)
		}
		bootstrapPeerID = info.PeerID.String()
	}

	// Ensure bootstrap config - always regenerate to ensure template fixes are applied
	bootstrapConfigPath := filepath.Join(ce.debrosDir, "bootstrap.yaml")
		data := templates.BootstrapConfigData{
			NodeID:         "bootstrap",
			P2PPort:        4001,
			DataDir:        bootstrapDir,
			RQLiteHTTPPort: 5001,
			RQLiteRaftPort: 7001,
			ClusterAPIPort: 9094,
			IPFSAPIPort:    4501,
		}

		config, err := templates.RenderBootstrapConfig(data)
		if err != nil {
			return fmt.Errorf("failed to render bootstrap config: %w", err)
		}

		if err := os.WriteFile(bootstrapConfigPath, []byte(config), 0644); err != nil {
			return fmt.Errorf("failed to write bootstrap config: %w", err)
		}

		fmt.Printf("✓ Generated bootstrap.yaml\n")

	return nil
}

// ensureNode2And3 creates node2 and node3 configs
func (ce *ConfigEnsurer) ensureNode2And3() error {
	// Get bootstrap multiaddr for join
	bootstrapInfo, err := encryption.LoadIdentity(filepath.Join(ce.debrosDir, "bootstrap", "identity.key"))
	if err != nil {
		return fmt.Errorf("failed to load bootstrap identity: %w", err)
	}

	bootstrapMultiaddr := fmt.Sprintf("/ip4/127.0.0.1/tcp/4001/p2p/%s", bootstrapInfo.PeerID.String())

	nodes := []struct {
		name           string
		p2pPort        int
		rqliteHTTPPort int
		rqliteRaftPort int
		clusterAPIPort int
		ipfsAPIPort    int
	}{
		{"node2", 4002, 5002, 7002, 9104, 4502},
		{"node3", 4003, 5003, 7003, 9114, 4503},
	}

	for _, node := range nodes {
		nodeDir := filepath.Join(ce.debrosDir, node.name)
		configPath := filepath.Join(ce.debrosDir, fmt.Sprintf("%s.yaml", node.name))

		// Always regenerate to ensure template fixes are applied
			if err := os.MkdirAll(nodeDir, 0755); err != nil {
				return fmt.Errorf("failed to create %s directory: %w", node.name, err)
			}

			data := templates.NodeConfigData{
				NodeID:            node.name,
				P2PPort:           node.p2pPort,
				DataDir:           nodeDir,
				RQLiteHTTPPort:    node.rqliteHTTPPort,
				RQLiteRaftPort:    node.rqliteRaftPort,
				RQLiteJoinAddress: "localhost:7001",
				BootstrapPeers:    []string{bootstrapMultiaddr},
				ClusterAPIPort:    node.clusterAPIPort,
				IPFSAPIPort:       node.ipfsAPIPort,
			}

			config, err := templates.RenderNodeConfig(data)
			if err != nil {
				return fmt.Errorf("failed to render %s config: %w", node.name, err)
			}

			if err := os.WriteFile(configPath, []byte(config), 0644); err != nil {
				return fmt.Errorf("failed to write %s config: %w", node.name, err)
			}

			fmt.Printf("✓ Generated %s.yaml\n", node.name)
	}

	return nil
}

// ensureGateway creates gateway config
func (ce *ConfigEnsurer) ensureGateway() error {
	configPath := filepath.Join(ce.debrosDir, "gateway.yaml")

	// Always regenerate to ensure template fixes are applied
		// Get bootstrap multiaddr
		bootstrapInfo, err := encryption.LoadIdentity(filepath.Join(ce.debrosDir, "bootstrap", "identity.key"))
		if err != nil {
			return fmt.Errorf("failed to load bootstrap identity: %w", err)
		}

		bootstrapMultiaddr := fmt.Sprintf("/ip4/127.0.0.1/tcp/4001/p2p/%s", bootstrapInfo.PeerID.String())

		data := templates.GatewayConfigData{
			ListenPort:     6001,
			BootstrapPeers: []string{bootstrapMultiaddr},
			OlricServers:   []string{"127.0.0.1:3320"},
			ClusterAPIPort: 9094,
			IPFSAPIPort:    4501,
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
	configPath := filepath.Join(ce.debrosDir, "olric-config.yaml")

	// Always regenerate to ensure template fixes are applied
		data := templates.OlricConfigData{
			BindAddr:       "127.0.0.1",
			HTTPPort:       3320,
			MemberlistPort: 3322,
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
