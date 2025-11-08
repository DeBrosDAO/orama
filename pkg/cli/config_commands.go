package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/config"
	"github.com/DeBrosOfficial/network/pkg/encryption"
)

// HandleConfigCommand handles config management commands
func HandleConfigCommand(args []string) {
	if len(args) == 0 {
		showConfigHelp()
		return
	}

	subcommand := args[0]
	subargs := args[1:]

	switch subcommand {
	case "init":
		handleConfigInit(subargs)
	case "validate":
		handleConfigValidate(subargs)
	case "help":
		showConfigHelp()
	default:
		fmt.Fprintf(os.Stderr, "Unknown config subcommand: %s\n", subcommand)
		showConfigHelp()
		os.Exit(1)
	}
}

func showConfigHelp() {
	fmt.Printf("Config Management Commands\n\n")
	fmt.Printf("Usage: network-cli config <subcommand> [options]\n\n")
	fmt.Printf("Subcommands:\n")
	fmt.Printf("  init                      - Generate full network stack in ~/.debros (bootstrap + 2 nodes + gateway)\n")
	fmt.Printf("  validate --name <file>    - Validate a config file\n\n")
	fmt.Printf("Init Default Behavior (no --type):\n")
	fmt.Printf("  Generates bootstrap.yaml, node2.yaml, node3.yaml, gateway.yaml with:\n")
	fmt.Printf("  - Auto-generated identities for bootstrap, node2, node3\n")
	fmt.Printf("  - Correct bootstrap_peers and join addresses\n")
	fmt.Printf("  - Default ports: P2P 4001-4003, HTTP 5001-5003, Raft 7001-7003\n\n")
	fmt.Printf("Init Options:\n")
	fmt.Printf("  --type <type>             - Single config type: node, bootstrap, gateway (skips stack generation)\n")
	fmt.Printf("  --name <file>             - Output filename (default: depends on --type or 'stack' for full stack)\n")
	fmt.Printf("  --force                   - Overwrite existing config/stack files\n\n")
	fmt.Printf("Single Config Options (with --type):\n")
	fmt.Printf("  --id <id>                 - Node ID for bootstrap peers\n")
	fmt.Printf("  --listen-port <port>      - LibP2P listen port (default: 4001)\n")
	fmt.Printf("  --rqlite-http-port <port> - RQLite HTTP port (default: 5001)\n")
	fmt.Printf("  --rqlite-raft-port <port> - RQLite Raft port (default: 7001)\n")
	fmt.Printf("  --join <host:port>        - RQLite address to join (required for non-bootstrap)\n")
	fmt.Printf("  --bootstrap-peers <peers> - Comma-separated bootstrap peer multiaddrs\n\n")
	fmt.Printf("Examples:\n")
	fmt.Printf("  network-cli config init                    # Generate full stack\n")
	fmt.Printf("  network-cli config init --force            # Overwrite existing stack\n")
	fmt.Printf("  network-cli config init --type bootstrap   # Single bootstrap config (legacy)\n")
	fmt.Printf("  network-cli config validate --name node.yaml\n")
}

func handleConfigInit(args []string) {
	// Parse flags
	var (
		cfgType        = ""
		name           = "" // Will be set based on type if not provided
		id             string
		listenPort     = 4001
		rqliteHTTPPort = 5001
		rqliteRaftPort = 7001
		joinAddr       string
		bootstrapPeers string
		force          bool
	)

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--type":
			if i+1 < len(args) {
				cfgType = args[i+1]
				i++
			}
		case "--name":
			if i+1 < len(args) {
				name = args[i+1]
				i++
			}
		case "--id":
			if i+1 < len(args) {
				id = args[i+1]
				i++
			}
		case "--listen-port":
			if i+1 < len(args) {
				if p, err := strconv.Atoi(args[i+1]); err == nil {
					listenPort = p
				}
				i++
			}
		case "--rqlite-http-port":
			if i+1 < len(args) {
				if p, err := strconv.Atoi(args[i+1]); err == nil {
					rqliteHTTPPort = p
				}
				i++
			}
		case "--rqlite-raft-port":
			if i+1 < len(args) {
				if p, err := strconv.Atoi(args[i+1]); err == nil {
					rqliteRaftPort = p
				}
				i++
			}
		case "--join":
			if i+1 < len(args) {
				joinAddr = args[i+1]
				i++
			}
		case "--bootstrap-peers":
			if i+1 < len(args) {
				bootstrapPeers = args[i+1]
				i++
			}
		case "--force":
			force = true
		}
	}

	// If --type is not specified, generate full stack
	if cfgType == "" {
		initFullStack(force)
		return
	}

	// Otherwise, continue with single-file generation
	// Validate type
	if cfgType != "node" && cfgType != "bootstrap" && cfgType != "gateway" {
		fmt.Fprintf(os.Stderr, "Invalid --type: %s (expected: node, bootstrap, or gateway)\n", cfgType)
		os.Exit(1)
	}

	// Set default name based on type if not provided
	if name == "" {
		switch cfgType {
		case "bootstrap":
			name = "bootstrap.yaml"
		case "gateway":
			name = "gateway.yaml"
		default:
			name = "node.yaml"
		}
	}

	// Ensure config directory exists
	configDir, err := config.EnsureConfigDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to ensure config directory: %v\n", err)
		os.Exit(1)
	}

	configPath := filepath.Join(configDir, name)

	// Check if file exists
	if !force {
		if _, err := os.Stat(configPath); err == nil {
			fmt.Fprintf(os.Stderr, "Config file already exists at %s (use --force to overwrite)\n", configPath)
			os.Exit(1)
		}
	}

	// Generate config based on type
	var configContent string
	switch cfgType {
	case "node":
		configContent = GenerateNodeConfig(name, id, listenPort, rqliteHTTPPort, rqliteRaftPort, joinAddr, bootstrapPeers)
	case "bootstrap":
		configContent = GenerateBootstrapConfig(name, id, listenPort, rqliteHTTPPort, rqliteRaftPort)
	case "gateway":
		configContent = GenerateGatewayConfig(bootstrapPeers)
	}

	// Write config file
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to write config file: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✅ Configuration file created: %s\n", configPath)
	fmt.Printf("   Type: %s\n", cfgType)
	fmt.Printf("\nYou can now start the %s using the generated config.\n", cfgType)
}

func handleConfigValidate(args []string) {
	var name string
	for i := 0; i < len(args); i++ {
		if args[i] == "--name" && i+1 < len(args) {
			name = args[i+1]
			i++
		}
	}

	if name == "" {
		fmt.Fprintf(os.Stderr, "Missing --name flag\n")
		showConfigHelp()
		os.Exit(1)
	}

	configDir, err := config.ConfigDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to get config directory: %v\n", err)
		os.Exit(1)
	}

	configPath := filepath.Join(configDir, name)
	file, err := os.Open(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open config file: %v\n", err)
		os.Exit(1)
	}
	defer file.Close()

	var cfg config.Config
	if err := config.DecodeStrict(file, &cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to parse config: %v\n", err)
		os.Exit(1)
	}

	// Run validation
	errs := cfg.Validate()
	if len(errs) > 0 {
		fmt.Fprintf(os.Stderr, "\n❌ Configuration errors (%d):\n", len(errs))
		for _, err := range errs {
			fmt.Fprintf(os.Stderr, "  - %s\n", err)
		}
		os.Exit(1)
	}

	fmt.Printf("✅ Config is valid: %s\n", configPath)
}

func initFullStack(force bool) {
	fmt.Printf("🚀 Initializing full network stack...\n")

	// Ensure ~/.debros directory exists
	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to get home directory: %v\n", err)
		os.Exit(1)
	}
	debrosDir := filepath.Join(homeDir, ".debros")
	if err := os.MkdirAll(debrosDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create ~/.debros directory: %v\n", err)
		os.Exit(1)
	}

	// Step 1: Generate bootstrap identity
	bootstrapIdentityDir := filepath.Join(debrosDir, "bootstrap")
	bootstrapIdentityPath := filepath.Join(bootstrapIdentityDir, "identity.key")

	if !force {
		if _, err := os.Stat(bootstrapIdentityPath); err == nil {
			fmt.Fprintf(os.Stderr, "Bootstrap identity already exists at %s (use --force to overwrite)\n", bootstrapIdentityPath)
			os.Exit(1)
		}
	}

	bootstrapInfo, err := encryption.GenerateIdentity()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to generate bootstrap identity: %v\n", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(bootstrapIdentityDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create bootstrap data directory: %v\n", err)
		os.Exit(1)
	}
	if err := encryption.SaveIdentity(bootstrapInfo, bootstrapIdentityPath); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to save bootstrap identity: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✅ Generated bootstrap identity: %s (Peer ID: %s)\n", bootstrapIdentityPath, bootstrapInfo.PeerID.String())

	// Construct bootstrap multiaddr
	bootstrapMultiaddr := fmt.Sprintf("/ip4/127.0.0.1/tcp/4001/p2p/%s", bootstrapInfo.PeerID.String())
	fmt.Printf("   Bootstrap multiaddr: %s\n", bootstrapMultiaddr)

	// Generate configs for all nodes...
	// (rest of the implementation - similar to what was in main.go)
	// I'll keep it similar to the original for consistency

	// Step 2: Generate bootstrap.yaml
	bootstrapName := "bootstrap.yaml"
	bootstrapPath := filepath.Join(debrosDir, bootstrapName)
	if !force {
		if _, err := os.Stat(bootstrapPath); err == nil {
			fmt.Fprintf(os.Stderr, "Bootstrap config already exists at %s (use --force to overwrite)\n", bootstrapPath)
			os.Exit(1)
		}
	}
	bootstrapContent := GenerateBootstrapConfig(bootstrapName, "", 4001, 5001, 7001)
	if err := os.WriteFile(bootstrapPath, []byte(bootstrapContent), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to write bootstrap config: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✅ Generated bootstrap config: %s\n", bootstrapPath)

	// Step 3: Generate node2.yaml
	node2Name := "node2.yaml"
	node2Path := filepath.Join(debrosDir, node2Name)
	if !force {
		if _, err := os.Stat(node2Path); err == nil {
			fmt.Fprintf(os.Stderr, "Node2 config already exists at %s (use --force to overwrite)\n", node2Path)
			os.Exit(1)
		}
	}
	node2Content := GenerateNodeConfig(node2Name, "", 4002, 5002, 7002, "localhost:5001", bootstrapMultiaddr)
	if err := os.WriteFile(node2Path, []byte(node2Content), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to write node2 config: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✅ Generated node2 config: %s\n", node2Path)

	// Step 4: Generate node3.yaml
	node3Name := "node3.yaml"
	node3Path := filepath.Join(debrosDir, node3Name)
	if !force {
		if _, err := os.Stat(node3Path); err == nil {
			fmt.Fprintf(os.Stderr, "Node3 config already exists at %s (use --force to overwrite)\n", node3Path)
			os.Exit(1)
		}
	}
	node3Content := GenerateNodeConfig(node3Name, "", 4003, 5003, 7003, "localhost:5001", bootstrapMultiaddr)
	if err := os.WriteFile(node3Path, []byte(node3Content), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to write node3 config: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✅ Generated node3 config: %s\n", node3Path)

	// Step 5: Generate gateway.yaml
	gatewayName := "gateway.yaml"
	gatewayPath := filepath.Join(debrosDir, gatewayName)
	if !force {
		if _, err := os.Stat(gatewayPath); err == nil {
			fmt.Fprintf(os.Stderr, "Gateway config already exists at %s (use --force to overwrite)\n", gatewayPath)
			os.Exit(1)
		}
	}
	gatewayContent := GenerateGatewayConfig(bootstrapMultiaddr)
	if err := os.WriteFile(gatewayPath, []byte(gatewayContent), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to write gateway config: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✅ Generated gateway config: %s\n", gatewayPath)

	fmt.Printf("\n" + strings.Repeat("=", 60) + "\n")
	fmt.Printf("✅ Full network stack initialized successfully!\n")
	fmt.Printf(strings.Repeat("=", 60) + "\n")
	fmt.Printf("\nBootstrap Peer ID: %s\n", bootstrapInfo.PeerID.String())
	fmt.Printf("Bootstrap Multiaddr: %s\n", bootstrapMultiaddr)
	fmt.Printf("\nGenerated configs:\n")
	fmt.Printf("  - %s\n", bootstrapPath)
	fmt.Printf("  - %s\n", node2Path)
	fmt.Printf("  - %s\n", node3Path)
	fmt.Printf("  - %s\n", gatewayPath)
	fmt.Printf("\nStart the network with: make dev\n")
}

// GenerateNodeConfig generates a node configuration
func GenerateNodeConfig(name, id string, listenPort, rqliteHTTPPort, rqliteRaftPort int, joinAddr, bootstrapPeers string) string {
	nodeID := id
	if nodeID == "" {
		nodeID = fmt.Sprintf("node-%d", time.Now().Unix())
	}

	// Parse bootstrap peers
	var peers []string
	if bootstrapPeers != "" {
		for _, p := range strings.Split(bootstrapPeers, ",") {
			if p = strings.TrimSpace(p); p != "" {
				peers = append(peers, p)
			}
		}
	}

	// Construct data_dir from name stem (remove .yaml)
	dataDir := strings.TrimSuffix(name, ".yaml")
	dataDir = filepath.Join(os.ExpandEnv("~"), ".debros", dataDir)

	var peersYAML strings.Builder
	if len(peers) == 0 {
		peersYAML.WriteString("  bootstrap_peers: []")
	} else {
		peersYAML.WriteString("  bootstrap_peers:\n")
		for _, p := range peers {
			fmt.Fprintf(&peersYAML, "    - \"%s\"\n", p)
		}
	}

	if joinAddr == "" {
		joinAddr = "localhost:5001"
	}

	// Calculate IPFS cluster API port (9094 for bootstrap, 9104+ for nodes)
	// Pattern: Bootstrap (5001) -> 9094, Node2 (5002) -> 9104, Node3 (5003) -> 9114
	clusterAPIPort := 9094 + (rqliteHTTPPort-5001)*10

	return fmt.Sprintf(`node:
  id: "%s"
  type: "node"
  listen_addresses:
    - "/ip4/0.0.0.0/tcp/%d"
  data_dir: "%s"
  max_connections: 50

database:
  data_dir: "%s/rqlite"
  replication_factor: 3
  shard_count: 16
  max_database_size: 1073741824
  backup_interval: "24h"
  rqlite_port: %d
  rqlite_raft_port: %d
  rqlite_join_address: "%s"
  cluster_sync_interval: "30s"
  peer_inactivity_limit: "24h"
  min_cluster_size: 1
  ipfs:
    # IPFS Cluster API endpoint for pin management (leave empty to disable)
    cluster_api_url: "http://localhost:%d"
    # IPFS HTTP API endpoint for content retrieval
    api_url: "http://localhost:%d"
    # Timeout for IPFS operations
    timeout: "60s"
    # Replication factor for pinned content
    replication_factor: 3
    # Enable client-side encryption before upload
    enable_encryption: true

discovery:
%s
  discovery_interval: "15s"
  bootstrap_port: %d
  http_adv_address: "localhost:%d"
  raft_adv_address: "localhost:%d"
  node_namespace: "default"

security:
  enable_tls: false

logging:
  level: "info"
  format: "console"
`, nodeID, listenPort, dataDir, dataDir, rqliteHTTPPort, rqliteRaftPort, joinAddr, clusterAPIPort, rqliteHTTPPort, peersYAML.String(), 4001, rqliteHTTPPort, rqliteRaftPort)
}

// GenerateBootstrapConfig generates a bootstrap configuration
func GenerateBootstrapConfig(name, id string, listenPort, rqliteHTTPPort, rqliteRaftPort int) string {
	nodeID := id
	if nodeID == "" {
		nodeID = "bootstrap"
	}

	dataDir := filepath.Join(os.ExpandEnv("~"), ".debros", "bootstrap")

	return fmt.Sprintf(`node:
  id: "%s"
  type: "bootstrap"
  listen_addresses:
    - "/ip4/0.0.0.0/tcp/%d"
  data_dir: "%s"
  max_connections: 50

database:
  data_dir: "%s/rqlite"
  replication_factor: 3
  shard_count: 16
  max_database_size: 1073741824
  backup_interval: "24h"
  rqlite_port: %d
  rqlite_raft_port: %d
  rqlite_join_address: ""
  cluster_sync_interval: "30s"
  peer_inactivity_limit: "24h"
  min_cluster_size: 1
  ipfs:
    # IPFS Cluster API endpoint for pin management (leave empty to disable)
    cluster_api_url: "http://localhost:9094"
    # IPFS HTTP API endpoint for content retrieval
    api_url: "http://localhost:%d"
    # Timeout for IPFS operations
    timeout: "60s"
    # Replication factor for pinned content
    replication_factor: 3
    # Enable client-side encryption before upload
    enable_encryption: true

discovery:
  bootstrap_peers: []
  discovery_interval: "15s"
  bootstrap_port: %d
  http_adv_address: "localhost:%d"
  raft_adv_address: "localhost:%d"
  node_namespace: "default"

security:
  enable_tls: false

logging:
  level: "info"
  format: "console"
`, nodeID, listenPort, dataDir, dataDir, rqliteHTTPPort, rqliteRaftPort, rqliteHTTPPort, 4001, rqliteHTTPPort, rqliteRaftPort)
}

// GenerateGatewayConfig generates a gateway configuration
func GenerateGatewayConfig(bootstrapPeers string) string {
	var peers []string
	if bootstrapPeers != "" {
		for _, p := range strings.Split(bootstrapPeers, ",") {
			if p = strings.TrimSpace(p); p != "" {
				peers = append(peers, p)
			}
		}
	}

	var peersYAML strings.Builder
	if len(peers) == 0 {
		peersYAML.WriteString("bootstrap_peers: []")
	} else {
		peersYAML.WriteString("bootstrap_peers:\n")
		for _, p := range peers {
			fmt.Fprintf(&peersYAML, "  - \"%s\"\n", p)
		}
	}

	return fmt.Sprintf(`listen_addr: ":6001"
client_namespace: "default"
rqlite_dsn: ""
%s
olric_servers:
  - "127.0.0.1:3320"
olric_timeout: "10s"
ipfs_cluster_api_url: "http://localhost:9094"
ipfs_api_url: "http://localhost:5001"
ipfs_timeout: "60s"
ipfs_replication_factor: 3
`, peersYAML.String())
}
