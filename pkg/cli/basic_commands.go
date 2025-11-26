package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/DeBrosOfficial/network/pkg/auth"
	"github.com/DeBrosOfficial/network/pkg/client"
)

// HandleHealthCommand handles the health command
func HandleHealthCommand(format string, timeout time.Duration) {
	cli, err := createClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create client: %v\n", err)
		os.Exit(1)
	}
	defer cli.Disconnect()

	health, err := cli.Health()
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

// HandlePeersCommand handles the peers command
func HandlePeersCommand(format string, timeout time.Duration) {
	cli, err := createClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create client: %v\n", err)
		os.Exit(1)
	}
	defer cli.Disconnect()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	peers, err := cli.Network().GetPeers(ctx)
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

// HandleStatusCommand handles the status command
func HandleStatusCommand(format string, timeout time.Duration) {
	cli, err := createClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create client: %v\n", err)
		os.Exit(1)
	}
	defer cli.Disconnect()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	status, err := cli.Network().GetStatus(ctx)
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

// HandleQueryCommand handles the query command
func HandleQueryCommand(sql, format string, timeout time.Duration) {
	// Ensure user is authenticated
	_ = ensureAuthenticated()

	cli, err := createClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create client: %v\n", err)
		os.Exit(1)
	}
	defer cli.Disconnect()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	result, err := cli.Database().Query(ctx, sql)
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

// HandleConnectCommand handles the connect command
func HandleConnectCommand(peerAddr string, timeout time.Duration) {
	cli, err := createClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create client: %v\n", err)
		os.Exit(1)
	}
	defer cli.Disconnect()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	err = cli.Network().ConnectToPeer(ctx, peerAddr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to connect to peer: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✅ Connected to peer: %s\n", peerAddr)
}

// HandlePeerIDCommand handles the peer-id command
func HandlePeerIDCommand(format string, timeout time.Duration) {
	cli, err := createClient()
	if err == nil {
		defer cli.Disconnect()
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		if status, err := cli.Network().GetStatus(ctx); err == nil {
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

// HandlePubSubCommand handles pubsub commands
func HandlePubSubCommand(args []string, format string, timeout time.Duration) {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "Usage: dbn pubsub <publish|subscribe|topics> [args...]\n")
		os.Exit(1)
	}

	// Ensure user is authenticated
	_ = ensureAuthenticated()

	cli, err := createClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create client: %v\n", err)
		os.Exit(1)
	}
	defer cli.Disconnect()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	subcommand := args[0]
	switch subcommand {
	case "publish":
		if len(args) < 3 {
			fmt.Fprintf(os.Stderr, "Usage: dbn pubsub publish <topic> <message>\n")
			os.Exit(1)
		}
		err := cli.PubSub().Publish(ctx, args[1], []byte(args[2]))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to publish message: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("✅ Published message to topic: %s\n", args[1])

	case "subscribe":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "Usage: dbn pubsub subscribe <topic> [duration]\n")
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

		err := cli.PubSub().Subscribe(ctx, args[1], messageHandler)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to subscribe: %v\n", err)
			os.Exit(1)
		}

		<-ctx.Done()
		fmt.Printf("✅ Subscription ended\n")

	case "topics":
		topics, err := cli.PubSub().ListTopics(ctx)
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

// Helper functions

func createClient() (client.NetworkClient, error) {
	config := client.DefaultClientConfig("dbn")

	// Use active environment's gateway URL
	gatewayURL := getGatewayURL()
	config.GatewayURL = gatewayURL

	// Try to get peer configuration from active environment
	env, err := GetActiveEnvironment()
	if err == nil && env != nil {
		// Environment loaded successfully - gateway URL already set above
		_ = env // Reserve for future peer configuration
	}

	// Check for existing credentials using enhanced authentication
	creds, err := auth.GetValidEnhancedCredentials()
	if err != nil {
		// No valid credentials found, use the enhanced authentication flow
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

func ensureAuthenticated() *auth.Credentials {
	gatewayURL := getGatewayURL()

	credentials, err := auth.GetOrPromptForCredentials(gatewayURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Authentication failed: %v\n", err)
		os.Exit(1)
	}

	return credentials
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
