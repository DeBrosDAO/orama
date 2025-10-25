package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/auth"
	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/config"
	"github.com/DeBrosOfficial/network/pkg/encryption"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

var (
	bootstrapPeer = "/ip4/127.0.0.1/tcp/4001"
	timeout       = 30 * time.Second
	format        = "table"
	useProduction = false
)

// version metadata populated via -ldflags at build time
var (
	version = "dev"
	commit  = ""
	date    = ""
)

func main() {
	if len(os.Args) < 2 {
		showHelp()
		return
	}

	command := os.Args[1]
	args := os.Args[2:]

	// Parse global flags
	parseGlobalFlags(args)

	switch command {
	case "version":
		fmt.Printf("network-cli %s", version)
		if commit != "" {
			fmt.Printf(" (commit %s)", commit)
		}
		if date != "" {
			fmt.Printf(" built %s", date)
		}
		fmt.Println()
		return
	case "health":
		handleHealth()
	case "peers":
		handlePeers()
	case "status":
		handleStatus()
	case "query":
		if len(args) == 0 {
			fmt.Fprintf(os.Stderr, "Usage: network-cli query <sql>\n")
			os.Exit(1)
		}
		handleQuery(args[0])
	case "pubsub":
		handlePubSub(args)
	case "connect":
		if len(args) == 0 {
			fmt.Fprintf(os.Stderr, "Usage: network-cli connect <peer_address>\n")
			os.Exit(1)
		}
		handleConnect(args[0])
	case "peer-id":
		handlePeerID()
	case "auth":
		handleAuth(args)
	case "config":
		handleConfig(args)
	case "help", "--help", "-h":
		showHelp()

	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", command)
		showHelp()
		os.Exit(1)
	}
}

func parseGlobalFlags(args []string) {
	for i, arg := range args {
		switch arg {
		case "-b", "--bootstrap":
			if i+1 < len(args) {
				bootstrapPeer = args[i+1]
			}
		case "-f", "--format":
			if i+1 < len(args) {
				format = args[i+1]
			}
		case "-t", "--timeout":
			if i+1 < len(args) {
				if d, err := time.ParseDuration(args[i+1]); err == nil {
					timeout = d
				}
			}
		case "--production":
			useProduction = true
		}
	}
}

func handleHealth() {
	client, err := createClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create client: %v\n", err)
		os.Exit(1)
	}
	defer client.Disconnect()

	health, err := client.Health()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to get health: %v\n", err)
		os.Exit(1)
	}

	if format == "json" {
		printJSON(health)
	} else {
		printHealth(health)
	}
}

func handlePeers() {
	client, err := createClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create client: %v\n", err)
		os.Exit(1)
	}
	defer client.Disconnect()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	peers, err := client.Network().GetPeers(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to get peers: %v\n", err)
		os.Exit(1)
	}

	if format == "json" {
		printJSON(peers)
	} else {
		printPeers(peers)
	}
}

func handleStatus() {
	client, err := createClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create client: %v\n", err)
		os.Exit(1)
	}
	defer client.Disconnect()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	status, err := client.Network().GetStatus(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to get status: %v\n", err)
		os.Exit(1)
	}

	if format == "json" {
		printJSON(status)
	} else {
		printStatus(status)
	}
}

func handleQuery(sql string) {
	// Ensure user is authenticated
	_ = ensureAuthenticated()

	client, err := createClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create client: %v\n", err)
		os.Exit(1)
	}
	defer client.Disconnect()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	result, err := client.Database().Query(ctx, sql)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to execute query: %v\n", err)
		os.Exit(1)
	}

	if format == "json" {
		printJSON(result)
	} else {
		printQueryResult(result)
	}
}

func handlePubSub(args []string) {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "Usage: network-cli pubsub <publish|subscribe|topics> [args...]\n")
		os.Exit(1)
	}

	// Ensure user is authenticated
	_ = ensureAuthenticated()

	client, err := createClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create client: %v\n", err)
		os.Exit(1)
	}
	defer client.Disconnect()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	subcommand := args[0]
	switch subcommand {
	case "publish":
		if len(args) < 3 {
			fmt.Fprintf(os.Stderr, "Usage: network-cli pubsub publish <topic> <message>\n")
			os.Exit(1)
		}
		err := client.PubSub().Publish(ctx, args[1], []byte(args[2]))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to publish message: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("✅ Published message to topic: %s\n", args[1])

	case "subscribe":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "Usage: network-cli pubsub subscribe <topic> [duration]\n")
			os.Exit(1)
		}
		duration := 30 * time.Second
		if len(args) > 2 {
			if d, err := time.ParseDuration(args[2]); err == nil {
				duration = d
			}
		}

		ctx, cancel := context.WithTimeout(context.Background(), duration)
		defer cancel()

		fmt.Printf("🔔 Subscribing to topic '%s' for %v...\n", args[1], duration)

		messageHandler := func(topic string, data []byte) error {
			fmt.Printf("📨 [%s] %s: %s\n", time.Now().Format("15:04:05"), topic, string(data))
			return nil
		}

		err := client.PubSub().Subscribe(ctx, args[1], messageHandler)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to subscribe: %v\n", err)
			os.Exit(1)
		}

		<-ctx.Done()
		fmt.Printf("✅ Subscription ended\n")

	case "topics":
		topics, err := client.PubSub().ListTopics(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to list topics: %v\n", err)
			os.Exit(1)
		}
		if format == "json" {
			printJSON(topics)
		} else {
			for _, topic := range topics {
				fmt.Println(topic)
			}
		}

	default:
		fmt.Fprintf(os.Stderr, "Unknown pubsub command: %s\n", subcommand)
		os.Exit(1)
	}
}

func handleAuth(args []string) {
	if len(args) == 0 {
		showAuthHelp()
		return
	}

	subcommand := args[0]
	switch subcommand {
	case "login":
		handleAuthLogin()
	case "logout":
		handleAuthLogout()
	case "whoami":
		handleAuthWhoami()
	case "status":
		handleAuthStatus()
	default:
		fmt.Fprintf(os.Stderr, "Unknown auth command: %s\n", subcommand)
		showAuthHelp()
		os.Exit(1)
	}
}

func handleAuthLogin() {
	gatewayURL := auth.GetDefaultGatewayURL()
	fmt.Printf("🔐 Authenticating with gateway at: %s\n", gatewayURL)

	// Use the wallet authentication flow
	creds, err := auth.PerformWalletAuthentication(gatewayURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Authentication failed: %v\n", err)
		os.Exit(1)
	}

	// Save credentials to file
	if err := auth.SaveCredentialsForDefaultGateway(creds); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to save credentials: %v\n", err)
		os.Exit(1)
	}

	credsPath, _ := auth.GetCredentialsPath()
	fmt.Printf("✅ Authentication successful!\n")
	fmt.Printf("📁 Credentials saved to: %s\n", credsPath)
	fmt.Printf("🎯 Wallet: %s\n", creds.Wallet)
	fmt.Printf("🏢 Namespace: %s\n", creds.Namespace)
}

func handleAuthLogout() {
	if err := auth.ClearAllCredentials(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to clear credentials: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("✅ Logged out successfully - all credentials have been cleared")
}

func handleAuthWhoami() {
	store, err := auth.LoadCredentials()
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to load credentials: %v\n", err)
		os.Exit(1)
	}

	gatewayURL := auth.GetDefaultGatewayURL()
	creds, exists := store.GetCredentialsForGateway(gatewayURL)

	if !exists || !creds.IsValid() {
		fmt.Println("❌ Not authenticated - run 'network-cli auth login' to authenticate")
		os.Exit(1)
	}

	fmt.Println("✅ Authenticated")
	fmt.Printf("  Wallet:    %s\n", creds.Wallet)
	fmt.Printf("  Namespace: %s\n", creds.Namespace)
	fmt.Printf("  Issued At: %s\n", creds.IssuedAt.Format("2006-01-02 15:04:05"))
	if !creds.ExpiresAt.IsZero() {
		fmt.Printf("  Expires At: %s\n", creds.ExpiresAt.Format("2006-01-02 15:04:05"))
	}
	if !creds.LastUsedAt.IsZero() {
		fmt.Printf("  Last Used: %s\n", creds.LastUsedAt.Format("2006-01-02 15:04:05"))
	}
	if creds.Plan != "" {
		fmt.Printf("  Plan:      %s\n", creds.Plan)
	}
}

func handleAuthStatus() {
	store, err := auth.LoadCredentials()
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to load credentials: %v\n", err)
		os.Exit(1)
	}

	gatewayURL := auth.GetDefaultGatewayURL()
	creds, exists := store.GetCredentialsForGateway(gatewayURL)

	fmt.Println("🔐 Authentication Status")
	fmt.Printf("  Gateway URL: %s\n", gatewayURL)

	if !exists || creds == nil {
		fmt.Println("  Status:     ❌ Not authenticated")
		return
	}

	if !creds.IsValid() {
		fmt.Println("  Status:     ⚠️  Credentials expired")
		if !creds.ExpiresAt.IsZero() {
			fmt.Printf("  Expired At: %s\n", creds.ExpiresAt.Format("2006-01-02 15:04:05"))
		}
		return
	}

	fmt.Println("  Status:     ✅ Authenticated")
	fmt.Printf("  Wallet:     %s\n", creds.Wallet)
	fmt.Printf("  Namespace:  %s\n", creds.Namespace)
	if !creds.ExpiresAt.IsZero() {
		fmt.Printf("  Expires:    %s\n", creds.ExpiresAt.Format("2006-01-02 15:04:05"))
	}
	if !creds.LastUsedAt.IsZero() {
		fmt.Printf("  Last Used:  %s\n", creds.LastUsedAt.Format("2006-01-02 15:04:05"))
	}
}

func showAuthHelp() {
	fmt.Printf("🔐 Authentication Commands\n\n")
	fmt.Printf("Usage: network-cli auth <subcommand>\n\n")
	fmt.Printf("Subcommands:\n")
	fmt.Printf("  login      - Authenticate with wallet\n")
	fmt.Printf("  logout     - Clear stored credentials\n")
	fmt.Printf("  whoami     - Show current authentication status\n")
	fmt.Printf("  status     - Show detailed authentication info\n\n")
	fmt.Printf("Examples:\n")
	fmt.Printf("  network-cli auth login\n")
	fmt.Printf("  network-cli auth whoami\n")
	fmt.Printf("  network-cli auth status\n")
	fmt.Printf("  network-cli auth logout\n\n")
	fmt.Printf("Environment Variables:\n")
	fmt.Printf("  DEBROS_GATEWAY_URL - Gateway URL (default: http://localhost:6001)\n")
}

func ensureAuthenticated() *auth.Credentials {
	gatewayURL := auth.GetDefaultGatewayURL()

	credentials, err := auth.GetOrPromptForCredentials(gatewayURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Authentication failed: %v\n", err)
		os.Exit(1)
	}

	return credentials
}

func openBrowser(target string) error {
	cmds := [][]string{
		{"xdg-open", target},
		{"open", target},
		{"cmd", "/c", "start", target},
	}
	for _, c := range cmds {
		cmd := exec.Command(c[0], c[1:]...)
		if err := cmd.Start(); err == nil {
			return nil
		}
	}
	log.Printf("Please open %s manually", target)
	return nil
}

func getenvDefault(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func handleConnect(peerAddr string) {
	client, err := createClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create client: %v\n", err)
		os.Exit(1)
	}
	defer client.Disconnect()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	err = client.Network().ConnectToPeer(ctx, peerAddr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to connect to peer: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✅ Connected to peer: %s\n", peerAddr)
}

func handlePeerID() {
	// Try to get peer ID from running network first
	client, err := createClient()
	if err == nil {
		defer client.Disconnect()
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		if status, err := client.Network().GetStatus(ctx); err == nil {
			if format == "json" {
				printJSON(map[string]string{"peer_id": status.NodeID})
			} else {
				fmt.Printf("🆔 Peer ID: %s\n", status.NodeID)
			}
			return
		}
	}

	fmt.Fprintf(os.Stderr, "❌ Could not find peer ID. Make sure the node is running or identity files exist.\n")
	os.Exit(1)
}

func createClient() (client.NetworkClient, error) {
	config := client.DefaultClientConfig("network-cli")

	// Check for existing credentials using enhanced authentication
	creds, err := auth.GetValidEnhancedCredentials()
	if err != nil {
		// No valid credentials found, use the enhanced authentication flow
		gatewayURL := auth.GetDefaultGatewayURL()

		newCreds, authErr := auth.GetOrPromptForCredentials(gatewayURL)
		if authErr != nil {
			return nil, fmt.Errorf("authentication failed: %w", authErr)
		}

		creds = newCreds
	}

	// Configure client with API key
	config.APIKey = creds.APIKey

	// Update last used time - the enhanced store handles saving automatically
	creds.UpdateLastUsed()

	networkClient, err := client.NewClient(config)
	if err != nil {
		return nil, err
	}

	if err := networkClient.Connect(); err != nil {
		return nil, err
	}

	return networkClient, nil
}

func discoverBootstrapPeer() string {
	// Look for peer info in common locations
	peerInfoPaths := []string{
		"./data/bootstrap/peer.info",
		"./data/test-bootstrap/peer.info",
		"/tmp/bootstrap-peer.info",
	}

	for _, path := range peerInfoPaths {
		if data, err := os.ReadFile(path); err == nil {
			peerAddr := strings.TrimSpace(string(data))
			if peerAddr != "" {
				// Only print discovery message in table format
				if format != "json" {
					fmt.Printf("🔍 Discovered bootstrap peer: %s\n", peerAddr)
				}
				return peerAddr
			}
		}
	}

	return "" // Return empty string if no peer info found
}

func isPrintableText(s string) bool {
	printableCount := 0
	for _, r := range s {
		if r >= 32 && r <= 126 || r == '\n' || r == '\r' || r == '\t' {
			printableCount++
		}
	}
	return len(s) > 0 && float64(printableCount)/float64(len(s)) > 0.8
}

func handleConfig(args []string) {
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
		configContent = generateNodeConfig(name, id, listenPort, rqliteHTTPPort, rqliteRaftPort, joinAddr, bootstrapPeers)
	case "bootstrap":
		configContent = generateBootstrapConfig(name, id, listenPort, rqliteHTTPPort, rqliteRaftPort)
	case "gateway":
		configContent = generateGatewayConfig(bootstrapPeers)
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

	// Step 2: Generate bootstrap.yaml
	bootstrapName := "bootstrap.yaml"
	bootstrapPath := filepath.Join(debrosDir, bootstrapName)
	if !force {
		if _, err := os.Stat(bootstrapPath); err == nil {
			fmt.Fprintf(os.Stderr, "Bootstrap config already exists at %s (use --force to overwrite)\n", bootstrapPath)
			os.Exit(1)
		}
	}
	bootstrapContent := generateBootstrapConfig(bootstrapName, "", 4001, 5001, 7001)
	if err := os.WriteFile(bootstrapPath, []byte(bootstrapContent), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to write bootstrap config: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✅ Generated bootstrap config: %s\n", bootstrapPath)

	// Step 3: Generate node2 identity and config
	node2IdentityDir := filepath.Join(debrosDir, "node2")
	node2IdentityPath := filepath.Join(node2IdentityDir, "identity.key")

	if !force {
		if _, err := os.Stat(node2IdentityPath); err == nil {
			fmt.Fprintf(os.Stderr, "Node2 identity already exists at %s (use --force to overwrite)\n", node2IdentityPath)
			os.Exit(1)
		}
	}

	node2Info, err := encryption.GenerateIdentity()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to generate node2 identity: %v\n", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(node2IdentityDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create node2 data directory: %v\n", err)
		os.Exit(1)
	}
	if err := encryption.SaveIdentity(node2Info, node2IdentityPath); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to save node2 identity: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✅ Generated node2 identity: %s (Peer ID: %s)\n", node2IdentityPath, node2Info.PeerID.String())

	node2Name := "node2.yaml"
	node2Path := filepath.Join(debrosDir, node2Name)
	if !force {
		if _, err := os.Stat(node2Path); err == nil {
			fmt.Fprintf(os.Stderr, "Node2 config already exists at %s (use --force to overwrite)\n", node2Path)
			os.Exit(1)
		}
	}
	node2Content := generateNodeConfig(node2Name, "", 4002, 5002, 7002, "127.0.0.1:7001", bootstrapMultiaddr)
	if err := os.WriteFile(node2Path, []byte(node2Content), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to write node2 config: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✅ Generated node2 config: %s\n", node2Path)

	// Step 4: Generate node3 identity and config
	node3IdentityDir := filepath.Join(debrosDir, "node3")
	node3IdentityPath := filepath.Join(node3IdentityDir, "identity.key")

	if !force {
		if _, err := os.Stat(node3IdentityPath); err == nil {
			fmt.Fprintf(os.Stderr, "Node3 identity already exists at %s (use --force to overwrite)\n", node3IdentityPath)
			os.Exit(1)
		}
	}

	node3Info, err := encryption.GenerateIdentity()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to generate node3 identity: %v\n", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(node3IdentityDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create node3 data directory: %v\n", err)
		os.Exit(1)
	}
	if err := encryption.SaveIdentity(node3Info, node3IdentityPath); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to save node3 identity: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✅ Generated node3 identity: %s (Peer ID: %s)\n", node3IdentityPath, node3Info.PeerID.String())

	node3Name := "node3.yaml"
	node3Path := filepath.Join(debrosDir, node3Name)
	if !force {
		if _, err := os.Stat(node3Path); err == nil {
			fmt.Fprintf(os.Stderr, "Node3 config already exists at %s (use --force to overwrite)\n", node3Path)
			os.Exit(1)
		}
	}
	node3Content := generateNodeConfig(node3Name, "", 4003, 5003, 7003, "127.0.0.1:7001", bootstrapMultiaddr)
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
	gatewayContent := generateGatewayConfig(bootstrapMultiaddr)
	if err := os.WriteFile(gatewayPath, []byte(gatewayContent), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to write gateway config: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✅ Generated gateway config: %s\n", gatewayPath)

	// Print summary
	fmt.Printf("\n" + strings.Repeat("=", 60) + "\n")
	fmt.Printf("✅ Full network stack initialized successfully!\n")
	fmt.Printf(strings.Repeat("=", 60) + "\n\n")
	fmt.Printf("Configuration files created in: %s\n\n", debrosDir)
	fmt.Printf("Bootstrap Node:\n")
	fmt.Printf("  Config:  %s\n", bootstrapPath)
	fmt.Printf("  Peer ID: %s\n", bootstrapInfo.PeerID.String())
	fmt.Printf("  Ports:   P2P=4001, HTTP=5001, Raft=7001\n\n")
	fmt.Printf("Node2:\n")
	fmt.Printf("  Config:  %s\n", node2Path)
	fmt.Printf("  Ports:   P2P=4002, HTTP=5002, Raft=7002\n")
	fmt.Printf("  Join:    127.0.0.1:7001\n\n")
	fmt.Printf("Node3:\n")
	fmt.Printf("  Config:  %s\n", node3Path)
	fmt.Printf("  Ports:   P2P=4003, HTTP=5003, Raft=7003\n")
	fmt.Printf("  Join:    127.0.0.1:7001\n\n")
	fmt.Printf("Gateway:\n")
	fmt.Printf("  Config:  %s\n\n", gatewayPath)
	fmt.Printf("To start the network:\n")
	fmt.Printf("  Terminal 1: ./bin/node --config bootstrap.yaml\n")
	fmt.Printf("  Terminal 2: ./bin/node --config node2.yaml\n")
	fmt.Printf("  Terminal 3: ./bin/node --config node3.yaml\n")
	fmt.Printf("  Terminal 4: ./bin/gateway --config gateway.yaml\n")
	fmt.Printf("\n" + strings.Repeat("=", 60) + "\n")
}

func generateNodeConfig(name, id string, listenPort, rqliteHTTPPort, rqliteRaftPort int, joinAddr, bootstrapPeers string) string {
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

discovery:
%s
  discovery_interval: "15s"
  bootstrap_port: %d
  http_adv_address: "127.0.0.1:%d"
  raft_adv_address: "127.0.0.1:%d"
  node_namespace: "default"

security:
  enable_tls: false

logging:
  level: "info"
  format: "console"
`, nodeID, listenPort, dataDir, dataDir, rqliteHTTPPort, rqliteRaftPort, joinAddr, peersYAML.String(), 4001, rqliteHTTPPort, rqliteRaftPort)
}

func generateBootstrapConfig(name, id string, listenPort, rqliteHTTPPort, rqliteRaftPort int) string {
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

discovery:
  bootstrap_peers: []
  discovery_interval: "15s"
  bootstrap_port: %d
  http_adv_address: "127.0.0.1:%d"
  raft_adv_address: "127.0.0.1:%d"
  node_namespace: "default"

security:
  enable_tls: false

logging:
  level: "info"
  format: "console"
`, nodeID, listenPort, dataDir, dataDir, rqliteHTTPPort, rqliteRaftPort, 4001, rqliteHTTPPort, rqliteRaftPort)
}

func generateGatewayConfig(bootstrapPeers string) string {
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
`, peersYAML.String())
}

func showHelp() {
	fmt.Printf("Network CLI - Distributed P2P Network Management Tool\n\n")
	fmt.Printf("Usage: network-cli <command> [args...]\n\n")
	fmt.Printf("🔐 Authentication: Commands requiring authentication will automatically prompt for wallet connection.\n\n")
	fmt.Printf("Commands:\n")
	fmt.Printf("  auth <subcommand>         🔐 Authentication management (login, logout, whoami, status)\n")
	fmt.Printf("  health                    - Check network health\n")
	fmt.Printf("  peers                     - List connected peers\n")
	fmt.Printf("  status                    - Show network status\n")
	fmt.Printf("  peer-id                   - Show this node's peer ID\n")
	fmt.Printf("  query <sql>               🔐 Execute database query\n")
	fmt.Printf("  pubsub publish <topic> <msg> 🔐 Publish message\n")
	fmt.Printf("  pubsub subscribe <topic> [duration] 🔐 Subscribe to topic\n")
	fmt.Printf("  pubsub topics             🔐 List topics\n")
	fmt.Printf("  connect <peer_address>    - Connect to peer\n")
	fmt.Printf("  config                    - Show current configuration\n")

	fmt.Printf("  help                      - Show this help\n\n")
	fmt.Printf("Global Flags:\n")
	fmt.Printf("  -b, --bootstrap <addr>    - Bootstrap peer address (default: /ip4/127.0.0.1/tcp/4001)\n")
	fmt.Printf("  -f, --format <format>     - Output format: table, json (default: table)\n")
	fmt.Printf("  -t, --timeout <duration>  - Operation timeout (default: 30s)\n")
	fmt.Printf("  --production              - Connect to production bootstrap peers\n\n")
	fmt.Printf("Authentication:\n")
	fmt.Printf("  Use 'network-cli auth login' to authenticate with your wallet\n")
	fmt.Printf("  Commands marked with 🔐 will automatically prompt for wallet authentication\n")
	fmt.Printf("  if no valid credentials are found. You can manage multiple wallets and\n")
	fmt.Printf("  choose between them during the authentication flow.\n\n")
	fmt.Printf("Examples:\n")
	fmt.Printf("  network-cli auth login\n")
	fmt.Printf("  network-cli auth whoami\n")
	fmt.Printf("  network-cli health\n")
	fmt.Printf("  network-cli peer-id\n")
	fmt.Printf("  network-cli peer-id --format json\n")
	fmt.Printf("  network-cli peers --format json\n")
	fmt.Printf("  network-cli peers --production\n")
	fmt.Printf("  ./bin/network-cli pubsub publish notifications \"Hello World\"\n")
}

func printHealth(health *client.HealthStatus) {
	fmt.Printf("🏥 Network Health\n")
	fmt.Printf("Status: %s\n", getStatusEmoji(health.Status)+health.Status)
	fmt.Printf("Last Updated: %s\n", health.LastUpdated.Format("2006-01-02 15:04:05"))
	fmt.Printf("Response Time: %v\n", health.ResponseTime)
	fmt.Printf("\nChecks:\n")
	for check, status := range health.Checks {
		emoji := "✅"
		if status != "ok" {
			emoji = "❌"
		}
		fmt.Printf("  %s %s: %s\n", emoji, check, status)
	}
}

func printPeers(peers []client.PeerInfo) {
	fmt.Printf("👥 Connected Peers (%d)\n\n", len(peers))
	if len(peers) == 0 {
		fmt.Printf("No peers connected\n")
		return
	}

	for i, peer := range peers {
		connEmoji := "🔴"
		if peer.Connected {
			connEmoji = "🟢"
		}
		fmt.Printf("%d. %s %s\n", i+1, connEmoji, peer.ID)
		fmt.Printf("   Addresses: %v\n", peer.Addresses)
		fmt.Printf("   Last Seen: %s\n", peer.LastSeen.Format("2006-01-02 15:04:05"))
		fmt.Println()
	}
}

func printStatus(status *client.NetworkStatus) {
	fmt.Printf("🌐 Network Status\n")
	fmt.Printf("Node ID: %s\n", status.NodeID)
	fmt.Printf("Connected: %s\n", getBoolEmoji(status.Connected)+strconv.FormatBool(status.Connected))
	fmt.Printf("Peer Count: %d\n", status.PeerCount)
	fmt.Printf("Database Size: %s\n", formatBytes(status.DatabaseSize))
	fmt.Printf("Uptime: %v\n", status.Uptime.Round(time.Second))
}

func printQueryResult(result *client.QueryResult) {
	fmt.Printf("📊 Query Result\n")
	fmt.Printf("Rows: %d\n\n", result.Count)

	if len(result.Rows) == 0 {
		fmt.Printf("No data returned\n")
		return
	}

	// Print header
	for i, col := range result.Columns {
		if i > 0 {
			fmt.Printf(" | ")
		}
		fmt.Printf("%-15s", col)
	}
	fmt.Println()

	// Print separator
	for i := range result.Columns {
		if i > 0 {
			fmt.Printf("-+-")
		}
		fmt.Printf("%-15s", "---------------")
	}
	fmt.Println()

	// Print rows
	for _, row := range result.Rows {
		for i, cell := range row {
			if i > 0 {
				fmt.Printf(" | ")
			}
			fmt.Printf("%-15v", cell)
		}
		fmt.Println()
	}
}

func printJSON(data interface{}) {
	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to marshal JSON: %v\n", err)
		return
	}
	fmt.Println(string(jsonData))
}

// Helper functions

func getStatusEmoji(status string) string {
	switch status {
	case "healthy":
		return "🟢 "
	case "degraded":
		return "🟡 "
	case "unhealthy":
		return "🔴 "
	default:
		return "⚪ "
	}
}

func getBoolEmoji(b bool) string {
	if b {
		return "✅ "
	}
	return "❌ "
}

func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

// extractPeerIDFromFile extracts peer ID from an identity key file
func extractPeerIDFromFile(keyFile string) string {
	// Read the identity key file
	data, err := os.ReadFile(keyFile)
	if err != nil {
		return ""
	}

	// Unmarshal the private key
	priv, err := crypto.UnmarshalPrivateKey(data)
	if err != nil {
		return ""
	}

	// Get the public key
	pub := priv.GetPublic()

	// Get the peer ID
	peerID, err := peer.IDFromPublicKey(pub)
	if err != nil {
		return ""
	}

	return peerID.String()
}

// extractPeerIDFromMultiaddr extracts the peer ID from a multiaddr string
func extractPeerIDFromMultiaddr(multiaddr string) string {
	// Look for /p2p/ followed by the peer ID
	parts := strings.Split(multiaddr, "/p2p/")
	if len(parts) >= 2 {
		return parts[1]
	}
	return ""
}
