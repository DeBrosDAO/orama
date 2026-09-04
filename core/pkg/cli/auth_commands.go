package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/auth"
)

// HandleAuthLogin authenticates with a wallet. Flags are parsed by cobra.
func HandleAuthLogin(wallet, namespace string, simple bool) {
	handleAuthLogin(wallet, namespace, simple)
}

// HandleAuthCommand handles authentication commands
func HandleAuthCommand(args []string) {
	if len(args) == 0 {
		showAuthHelp()
		return
	}

	subcommand := args[0]
	switch subcommand {
	case "logout":
		handleAuthLogout()
	case "whoami":
		handleAuthWhoami()
	case "status":
		handleAuthStatus()
	case "list":
		handleAuthList()
	case "switch":
		handleAuthSwitch()
	default:
		fmt.Fprintf(os.Stderr, "Unknown auth command: %s\n", subcommand)
		showAuthHelp()
		os.Exit(1)
	}
}

func showAuthHelp() {
	fmt.Printf("🔐 Authentication Commands\n\n")
	fmt.Printf("Usage: orama auth <subcommand>\n\n")
	fmt.Printf("Subcommands:\n")
	fmt.Printf("  login      - Authenticate with RootWallet (default) or simple auth\n")
	fmt.Printf("  logout     - Clear stored credentials\n")
	fmt.Printf("  whoami     - Show current authentication status\n")
	fmt.Printf("  status     - Show detailed authentication info\n")
	fmt.Printf("  list       - List all stored credentials for current environment\n")
	fmt.Printf("  switch     - Switch between stored credentials\n\n")
	fmt.Printf("Login Flags:\n")
	fmt.Printf("  --namespace <name>  - Target namespace\n")
	fmt.Printf("  --simple            - Use simple auth (no signature, dev only)\n")
	fmt.Printf("  --wallet <0x...>    - Wallet address (implies --simple)\n\n")
	fmt.Printf("Examples:\n")
	fmt.Printf("  orama auth login                    # Sign with RootWallet (default)\n")
	fmt.Printf("  orama auth login --namespace myns   # Sign with RootWallet + namespace\n")
	fmt.Printf("  orama auth login --simple           # Simple auth (no signature)\n")
	fmt.Printf("  orama auth whoami                   # Check who you're logged in as\n")
	fmt.Printf("  orama auth logout                   # Clear all stored credentials\n\n")
	fmt.Printf("Environment Variables:\n")
	fmt.Printf("  ORAMA_GATEWAY_URL - Gateway URL (overrides environment config)\n\n")
	fmt.Printf("Authentication Flow (RootWallet):\n")
	fmt.Printf("  1. Run 'orama auth login'\n")
	fmt.Printf("  2. Your wallet address is read from RootWallet automatically\n")
	fmt.Printf("  3. Enter your namespace when prompted\n")
	fmt.Printf("  4. A challenge nonce is signed with your wallet key\n")
	fmt.Printf("  5. Credentials are saved to ~/.orama/credentials.json\n\n")
	fmt.Printf("Note: Requires RootWallet CLI (rw) in PATH.\n")
	fmt.Printf("      Install: cd rootwallet/cli && ./install.sh\n")
	fmt.Printf("      Authentication uses the currently active environment.\n")
	fmt.Printf("      Use 'orama env current' to see your active environment.\n")
}

func handleAuthLogin(wallet, namespace string, simple bool) {
	// Get gateway URL from active environment
	gatewayURL := getGatewayURL()

	// Show active environment
	env, err := GetActiveEnvironment()
	if err == nil {
		fmt.Printf("🌍 Environment: %s\n", env.Name)
	}
	fmt.Printf("🔐 Authenticating with gateway at: %s\n\n", gatewayURL)

	// Load enhanced credential store
	store, err := auth.LoadEnhancedCredentials()
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to load credentials: %v\n", err)
		os.Exit(1)
	}

	// Check if we already have credentials for this gateway
	gwCreds := store.Gateways[gatewayURL]
	if gwCreds != nil && len(gwCreds.Credentials) > 0 {
		// Show existing credentials and offer choice
		choice, credIndex, err := store.DisplayCredentialMenu(gatewayURL)
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ Menu selection failed: %v\n", err)
			os.Exit(1)
		}

		switch choice {
		case auth.AuthChoiceUseCredential:
			selectedCreds := gwCreds.Credentials[credIndex]
			store.SetDefaultCredential(gatewayURL, credIndex)
			selectedCreds.UpdateLastUsed()
			if err := store.Save(); err != nil {
				fmt.Fprintf(os.Stderr, "❌ Failed to save credentials: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("✅ Switched to wallet: %s\n", selectedCreds.Wallet)
			fmt.Printf("🏢 Namespace: %s\n", selectedCreds.Namespace)
			return

		case auth.AuthChoiceLogout:
			store.ClearAllCredentials()
			if err := store.Save(); err != nil {
				fmt.Fprintf(os.Stderr, "❌ Failed to clear credentials: %v\n", err)
				os.Exit(1)
			}
			fmt.Println("✅ All credentials cleared")
			return

		case auth.AuthChoiceExit:
			fmt.Println("Exiting...")
			return

		case auth.AuthChoiceAddCredential:
			// Fall through to add new credential
		}
	}

	// Choose authentication method
	var creds *auth.Credentials
	reader := bufio.NewReader(os.Stdin)

	if simple || wallet != "" {
		// Explicit simple auth — requires existing credentials
		existingCreds := store.GetDefaultCredential(gatewayURL)
		if existingCreds == nil || !existingCreds.IsValid() {
			fmt.Fprintf(os.Stderr, "❌ Simple auth requires existing credentials. Authenticate with RootWallet or Phantom first.\n")
			os.Exit(1)
		}
		creds, err = auth.PerformSimpleAuthentication(gatewayURL, wallet, namespace, existingCreds.APIKey)
	} else {
		// Show auth method selection
		fmt.Println("How would you like to authenticate?")
		fmt.Println("  1. RootWallet (EVM signature)")
		fmt.Println("  2. Phantom (Solana + NFT required)")
		fmt.Print("\nSelect [1/2]: ")

		choice, _ := reader.ReadString('\n')
		choice = strings.TrimSpace(choice)

		switch choice {
		case "2":
			creds, err = auth.PerformPhantomAuthentication(gatewayURL, namespace)
		default:
			// Default to RootWallet
			if auth.IsRootWalletInstalled() {
				creds, err = auth.PerformRootWalletAuthentication(gatewayURL, namespace)
			} else {
				fmt.Println("\n⚠️  RootWallet CLI (rw) not found in PATH.")
				fmt.Println("   Install it: cd rootwallet/cli && ./install.sh")
				os.Exit(1)
			}
		}
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Authentication failed: %v\n", err)
		os.Exit(1)
	}

	// Add to enhanced store
	store.AddCredential(gatewayURL, creds)

	// Set as default
	gwCreds = store.Gateways[gatewayURL]
	if gwCreds != nil {
		store.SetDefaultCredential(gatewayURL, len(gwCreds.Credentials)-1)
	}

	if err := store.Save(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to save credentials: %v\n", err)
		os.Exit(1)
	}

	credsPath, _ := auth.GetCredentialsPath()
	fmt.Printf("✅ Authentication successful!\n")
	fmt.Printf("📁 Credentials saved to: %s\n", credsPath)
	fmt.Printf("🎯 Wallet: %s\n", creds.Wallet)
	fmt.Printf("🏢 Namespace: %s\n", creds.Namespace)
	if creds.NamespaceURL != "" {
		fmt.Printf("🌐 Namespace URL: %s\n", creds.NamespaceURL)
	}
}

func handleAuthLogout() {
	if err := auth.ClearAllCredentials(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to clear credentials: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("✅ Logged out successfully - all credentials have been cleared")
}

func handleAuthWhoami() {
	store, err := auth.LoadEnhancedCredentials()
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to load credentials: %v\n", err)
		os.Exit(1)
	}

	gatewayURL := getGatewayURL()
	creds := store.GetDefaultCredential(gatewayURL)

	if creds == nil || !creds.IsValid() {
		fmt.Println("❌ Not authenticated - run 'orama auth login' to authenticate")
		os.Exit(1)
	}

	fmt.Println("✅ Authenticated")
	fmt.Printf("  Wallet:    %s\n", creds.Wallet)
	fmt.Printf("  Namespace: %s\n", creds.Namespace)
	if creds.NamespaceURL != "" {
		fmt.Printf("  NS Gateway: %s\n", creds.NamespaceURL)
	}
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
	store, err := auth.LoadEnhancedCredentials()
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to load credentials: %v\n", err)
		os.Exit(1)
	}

	gatewayURL := getGatewayURL()
	creds := store.GetDefaultCredential(gatewayURL)

	// Show active environment
	env, err := GetActiveEnvironment()
	if err == nil {
		fmt.Printf("🌍 Active Environment: %s\n", env.Name)
	}

	fmt.Println("🔐 Authentication Status")
	fmt.Printf("  Gateway URL: %s\n", gatewayURL)

	if creds == nil {
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
	if creds.NamespaceURL != "" {
		fmt.Printf("  NS Gateway: %s\n", creds.NamespaceURL)
	}
	if !creds.ExpiresAt.IsZero() {
		fmt.Printf("  Expires:    %s\n", creds.ExpiresAt.Format("2006-01-02 15:04:05"))
	}
	if !creds.LastUsedAt.IsZero() {
		fmt.Printf("  Last Used:  %s\n", creds.LastUsedAt.Format("2006-01-02 15:04:05"))
	}
}

// promptForGatewayURL interactively prompts for the gateway URL
// Uses the active environment or allows entering a custom domain
func promptForGatewayURL() string {
	// Check environment variable first (allows override without prompting)
	if url := os.Getenv("ORAMA_GATEWAY_URL"); url != "" {
		return url
	}

	// Try active environment
	env, err := GetActiveEnvironment()
	if err == nil {
		reader := bufio.NewReader(os.Stdin)

		fmt.Println("\n🌐 Node Connection")
		fmt.Println("==================")
		fmt.Printf("1. Use active environment: %s (%s)\n", env.Name, env.GatewayURL)
		fmt.Println("2. Enter custom domain")
		fmt.Print("\nSelect option [1/2]: ")

		choice, _ := reader.ReadString('\n')
		choice = strings.TrimSpace(choice)

		if choice == "1" || choice == "" {
			return env.GatewayURL
		}

		if choice == "2" {
			fmt.Print("Enter node domain (e.g., node-hk19de.orama.network): ")
			domain, _ := reader.ReadString('\n')
			domain = strings.TrimSpace(domain)

			if domain == "" {
				fmt.Printf("⚠️  No domain entered, using %s\n", env.Name)
				return env.GatewayURL
			}

			// Remove any protocol prefix if user included it
			domain = strings.TrimPrefix(domain, "https://")
			domain = strings.TrimPrefix(domain, "http://")
			// Remove trailing slash
			domain = strings.TrimSuffix(domain, "/")

			return fmt.Sprintf("https://%s", domain)
		}

		return env.GatewayURL
	}

	return "https://orama-devnet.network"
}

// getGatewayURL returns the gateway URL based on environment or env var
// Used by other commands that don't need interactive node selection
// getGatewayURL resolves the gateway for the auth and namespace commands.
//
// It goes through auth.ResolveGatewayURL like every other command, so these
// commands cannot end up pointed at a different gateway than the one whose
// credential they read. It previously honoured only ORAMA_GATEWAY_URL and fell
// back to a hardcoded devnet URL, which meant an unconfigured shell silently
// talked to a live network.
//
// Reporting the failure by exiting matches how these handlers report every
// other error; chg-336 converts the whole file to returned errors.
func getGatewayURL() string {
	url, err := auth.ResolveGatewayURL()
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		os.Exit(1)
	}
	return url
}

func handleAuthList() {
	store, err := auth.LoadEnhancedCredentials()
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to load credentials: %v\n", err)
		os.Exit(1)
	}

	gatewayURL := getGatewayURL()

	// Show active environment
	env, err := GetActiveEnvironment()
	if err == nil {
		fmt.Printf("🌍 Environment: %s\n", env.Name)
	}
	fmt.Printf("🔗 Gateway: %s\n\n", gatewayURL)

	gwCreds := store.Gateways[gatewayURL]
	if gwCreds == nil || len(gwCreds.Credentials) == 0 {
		fmt.Println("No credentials stored for this environment.")
		fmt.Println("Run 'orama auth login' to authenticate.")
		return
	}

	fmt.Printf("🔐 Stored Credentials (%d):\n\n", len(gwCreds.Credentials))
	for i, creds := range gwCreds.Credentials {
		defaultMark := ""
		if i == gwCreds.DefaultIndex {
			defaultMark = " ← active"
		}

		statusEmoji := "✅"
		statusText := "valid"
		if !creds.IsValid() {
			statusEmoji = "❌"
			statusText = "expired"
		}

		fmt.Printf("  %d. %s Wallet: %s%s\n", i+1, statusEmoji, creds.Wallet, defaultMark)
		fmt.Printf("     Namespace: %s | Status: %s\n", creds.Namespace, statusText)
		if creds.Plan != "" {
			fmt.Printf("     Plan: %s\n", creds.Plan)
		}
		if !creds.IssuedAt.IsZero() {
			fmt.Printf("     Issued: %s\n", creds.IssuedAt.Format("2006-01-02 15:04:05"))
		}
		fmt.Println()
	}
}

func handleAuthSwitch() {
	store, err := auth.LoadEnhancedCredentials()
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to load credentials: %v\n", err)
		os.Exit(1)
	}

	gatewayURL := getGatewayURL()

	gwCreds := store.Gateways[gatewayURL]
	if gwCreds == nil || len(gwCreds.Credentials) == 0 {
		fmt.Println("No credentials stored for this environment.")
		fmt.Println("Run 'orama auth login' to authenticate first.")
		os.Exit(1)
	}

	if len(gwCreds.Credentials) == 1 {
		fmt.Println("Only one credential stored. Nothing to switch to.")
		return
	}

	// Display menu
	choice, credIndex, err := store.DisplayCredentialMenu(gatewayURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Menu selection failed: %v\n", err)
		os.Exit(1)
	}

	switch choice {
	case auth.AuthChoiceUseCredential:
		selectedCreds := gwCreds.Credentials[credIndex]
		store.SetDefaultCredential(gatewayURL, credIndex)
		selectedCreds.UpdateLastUsed()
		if err := store.Save(); err != nil {
			fmt.Fprintf(os.Stderr, "❌ Failed to save credentials: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("✅ Switched to wallet: %s\n", selectedCreds.Wallet)
		fmt.Printf("🏢 Namespace: %s\n", selectedCreds.Namespace)

	case auth.AuthChoiceAddCredential:
		fmt.Println("Use 'orama auth login' to add a new credential.")

	case auth.AuthChoiceLogout:
		store.ClearAllCredentials()
		if err := store.Save(); err != nil {
			fmt.Fprintf(os.Stderr, "❌ Failed to clear credentials: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("✅ All credentials cleared")

	case auth.AuthChoiceExit:
		fmt.Println("Cancelled.")
	}
}
