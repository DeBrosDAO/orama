package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/auth"
)

// HandleAuthCommand handles authentication commands
func HandleAuthCommand(args []string) {
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

func showAuthHelp() {
	fmt.Printf("🔐 Authentication Commands\n\n")
	fmt.Printf("Usage: dbn auth <subcommand>\n\n")
	fmt.Printf("Subcommands:\n")
	fmt.Printf("  login      - Authenticate by providing your wallet address\n")
	fmt.Printf("  logout     - Clear stored credentials\n")
	fmt.Printf("  whoami     - Show current authentication status\n")
	fmt.Printf("  status     - Show detailed authentication info\n\n")
	fmt.Printf("Examples:\n")
	fmt.Printf("  dbn auth login          # Enter wallet address interactively\n")
	fmt.Printf("  dbn auth whoami         # Check who you're logged in as\n")
	fmt.Printf("  dbn auth status         # View detailed authentication info\n")
	fmt.Printf("  dbn auth logout         # Clear all stored credentials\n\n")
	fmt.Printf("Environment Variables:\n")
	fmt.Printf("  DEBROS_GATEWAY_URL - Gateway URL (overrides environment config)\n\n")
	fmt.Printf("Authentication Flow:\n")
	fmt.Printf("  1. Run 'dbn auth login'\n")
	fmt.Printf("  2. Enter your wallet address when prompted\n")
	fmt.Printf("  3. Enter your namespace (or press Enter for 'default')\n")
	fmt.Printf("  4. An API key will be generated and saved to ~/.orama/credentials.json\n\n")
	fmt.Printf("Note: Authentication uses the currently active environment.\n")
	fmt.Printf("      Use 'dbn env current' to see your active environment.\n")
}

func handleAuthLogin() {
	// Prompt for node selection
	gatewayURL := promptForGatewayURL()
	fmt.Printf("🔐 Authenticating with gateway at: %s\n", gatewayURL)

	// Use the simple authentication flow
	creds, err := auth.PerformSimpleAuthentication(gatewayURL)
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
	fmt.Printf("🔑 API Key: %s\n", creds.APIKey)
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

	gatewayURL := getGatewayURL()
	creds, exists := store.GetCredentialsForGateway(gatewayURL)

	if !exists || !creds.IsValid() {
		fmt.Println("❌ Not authenticated - run 'dbn auth login' to authenticate")
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

	gatewayURL := getGatewayURL()
	creds, exists := store.GetCredentialsForGateway(gatewayURL)

	// Show active environment
	env, err := GetActiveEnvironment()
	if err == nil {
		fmt.Printf("🌍 Active Environment: %s\n", env.Name)
	}

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

// promptForGatewayURL interactively prompts for the gateway URL
// Allows user to choose between local node or remote node by domain
func promptForGatewayURL() string {
	// Check environment variable first (allows override without prompting)
	if url := os.Getenv("DEBROS_GATEWAY_URL"); url != "" {
		return url
	}

	reader := bufio.NewReader(os.Stdin)

	fmt.Println("\n🌐 Node Connection")
	fmt.Println("==================")
	fmt.Println("1. Local node (localhost:6001)")
	fmt.Println("2. Remote node (enter domain)")
	fmt.Print("\nSelect option [1/2]: ")

	choice, _ := reader.ReadString('\n')
	choice = strings.TrimSpace(choice)

	if choice == "1" || choice == "" {
		return "http://localhost:6001"
	}

	if choice != "2" {
		fmt.Println("⚠️  Invalid option, using localhost")
		return "http://localhost:6001"
	}

	fmt.Print("Enter node domain (e.g., node-hk19de.debros.network): ")
	domain, _ := reader.ReadString('\n')
	domain = strings.TrimSpace(domain)

	if domain == "" {
		fmt.Println("⚠️  No domain entered, using localhost")
		return "http://localhost:6001"
	}

	// Remove any protocol prefix if user included it
	domain = strings.TrimPrefix(domain, "https://")
	domain = strings.TrimPrefix(domain, "http://")
	// Remove trailing slash
	domain = strings.TrimSuffix(domain, "/")

	// Use HTTPS for remote domains
	return fmt.Sprintf("https://%s", domain)
}

// getGatewayURL returns the gateway URL based on environment or env var
// Used by other commands that don't need interactive node selection
func getGatewayURL() string {
	// Check environment variable first (for backwards compatibility)
	if url := os.Getenv("DEBROS_GATEWAY_URL"); url != "" {
		return url
	}

	// Get from active environment
	env, err := GetActiveEnvironment()
	if err == nil {
		return env.GatewayURL
	}

	// Fallback to default (node-1)
	return "http://localhost:6001"
}
