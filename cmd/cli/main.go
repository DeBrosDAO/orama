package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/auth"
	"github.com/DeBrosOfficial/network/pkg/client"
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

	// Fallback: try to extract from local identity files
	identityPaths := []string{
		"/opt/debros/data/node/identity.key",
		"/opt/debros/data/bootstrap/identity.key",
		"/opt/debros/keys/node/identity.key",
		"./data/node/identity.key",
		"./data/bootstrap/identity.key",
	}

	for _, path := range identityPaths {
		if peerID := extractPeerIDFromFile(path); peerID != "" {
			if format == "json" {
				printJSON(map[string]string{"peer_id": peerID, "source": "local_identity"})
			} else {
				fmt.Printf("🆔 Peer ID: %s\n", peerID)
				fmt.Printf("📂 Source: %s\n", path)
			}
			return
		}
	}

	// Check peer.info files as last resort
	peerInfoPaths := []string{
		"/opt/debros/data/node/peer.info",
		"/opt/debros/data/bootstrap/peer.info",
		"./data/node/peer.info",
		"./data/bootstrap/peer.info",
	}

	for _, path := range peerInfoPaths {
		if data, err := os.ReadFile(path); err == nil {
			multiaddr := strings.TrimSpace(string(data))
			if peerID := extractPeerIDFromMultiaddr(multiaddr); peerID != "" {
				if format == "json" {
					printJSON(map[string]string{"peer_id": peerID, "source": "peer_info"})
				} else {
					fmt.Printf("🆔 Peer ID: %s\n", peerID)
					fmt.Printf("📂 Source: %s\n", path)
				}
				return
			}
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

func tryDecodeBase64(s string) string {
	// Only try to decode if it looks like base64 (no spaces, reasonable length)
	if len(s) > 0 && len(s)%4 == 0 && !strings.ContainsAny(s, " \n\r\t") {
		if decoded, err := base64.StdEncoding.DecodeString(s); err == nil {
			// Check if decoded result looks like readable text
			decodedStr := string(decoded)
			if isPrintableText(decodedStr) {
				return decodedStr
			}
		}
	}
	return s
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

func showHelp() {
	fmt.Printf("Network CLI - Distributed P2P Network Management Tool\n\n")
	fmt.Printf("Usage: network-cli <command> [args...]\n\n")
	fmt.Printf("🔐 Authentication: Commands requiring authentication will automatically prompt for wallet connection.\n\n")
	fmt.Printf("Commands:\n")
	fmt.Printf("  health                    - Check network health\n")
	fmt.Printf("  peers                     - List connected peers\n")
	fmt.Printf("  status                    - Show network status\n")
	fmt.Printf("  peer-id                   - Show this node's peer ID\n")
	fmt.Printf("  query <sql>               🔐 Execute database query\n")
	fmt.Printf("  pubsub publish <topic> <msg> 🔐 Publish message\n")
	fmt.Printf("  pubsub subscribe <topic> [duration] 🔐 Subscribe to topic\n")
	fmt.Printf("  pubsub topics             🔐 List topics\n")
	fmt.Printf("  connect <peer_address>    - Connect to peer\n")

	fmt.Printf("  help                      - Show this help\n\n")
	fmt.Printf("Global Flags:\n")
	fmt.Printf("  -b, --bootstrap <addr>    - Bootstrap peer address (default: /ip4/127.0.0.1/tcp/4001)\n")
	fmt.Printf("  -f, --format <format>     - Output format: table, json (default: table)\n")
	fmt.Printf("  -t, --timeout <duration>  - Operation timeout (default: 30s)\n")
	fmt.Printf("  --production              - Connect to production bootstrap peers\n\n")
	fmt.Printf("Authentication:\n")
	fmt.Printf("  Commands marked with 🔐 will automatically prompt for wallet authentication\n")
	fmt.Printf("  if no valid credentials are found. You can manage multiple wallets and\n")
	fmt.Printf("  choose between them during the authentication flow.\n\n")
	fmt.Printf("Examples:\n")
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
