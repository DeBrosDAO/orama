package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/auth"
	"github.com/DeBrosOfficial/network/pkg/cli/clierr"
)

// AuthLogin authenticates with a wallet and stores the credential.
func AuthLogin(wallet, namespace string, simple bool) error {
	gatewayURL, err := getGatewayURL()
	if err != nil {
		return err
	}

	// Show active environment
	if env, envErr := GetActiveEnvironment(); envErr == nil {
		fmt.Printf("Environment: %s\n", env.Name)
	}
	fmt.Printf("Authenticating with gateway at: %s\n\n", gatewayURL)

	store, err := auth.LoadEnhancedCredentials()
	if err != nil {
		return clierr.Failure("failed to load credentials: %w", err)
	}

	// Check if we already have credentials for this gateway
	gwCreds := store.Gateways[gatewayURL]
	if gwCreds != nil && len(gwCreds.Credentials) > 0 {
		// Show existing credentials and offer choice
		choice, credIndex, menuErr := store.DisplayCredentialMenu(gatewayURL)
		if menuErr != nil {
			return clierr.Failure("menu selection failed: %w", menuErr)
		}

		switch choice {
		case auth.AuthChoiceUseCredential:
			selectedCreds := gwCreds.Credentials[credIndex]
			store.SetDefaultCredential(gatewayURL, credIndex)
			selectedCreds.UpdateLastUsed()
			if err := store.Save(); err != nil {
				return clierr.Failure("failed to save credentials: %w", err)
			}
			fmt.Printf("Switched to wallet: %s\n", selectedCreds.Wallet)
			fmt.Printf("Namespace: %s\n", selectedCreds.Namespace)
			return nil

		case auth.AuthChoiceLogout:
			store.ClearAllCredentials()
			if err := store.Save(); err != nil {
				return clierr.Failure("failed to clear credentials: %w", err)
			}
			fmt.Println("All credentials cleared")
			return nil

		case auth.AuthChoiceExit:
			return clierr.Aborted("cancelled")

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
			return clierr.Auth("simple auth requires existing credentials; authenticate with RootWallet first")
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
				return clierr.Usage("RootWallet CLI (rw) not found in PATH\n" +
					"  Install it: cd rootwallet/cli && ./install.sh")
			}
		}
	}

	if err != nil {
		return clierr.Auth("authentication failed: %w", err)
	}

	// Add to enhanced store
	store.AddCredential(gatewayURL, creds)

	// Set as default
	gwCreds = store.Gateways[gatewayURL]
	if gwCreds != nil {
		store.SetDefaultCredential(gatewayURL, len(gwCreds.Credentials)-1)
	}

	if err := store.Save(); err != nil {
		return clierr.Failure("failed to save credentials: %w", err)
	}

	credsPath, _ := auth.GetCredentialsPath()
	fmt.Printf("Authentication successful.\n")
	fmt.Printf("  Credentials saved to: %s\n", credsPath)
	fmt.Printf("  Wallet:    %s\n", creds.Wallet)
	fmt.Printf("  Namespace: %s\n", creds.Namespace)
	if creds.NamespaceURL != "" {
		fmt.Printf("  Namespace URL: %s\n", creds.NamespaceURL)
	}
	return nil
}

// AuthLogout clears every stored credential.
func AuthLogout() error {
	if err := auth.ClearAllCredentials(); err != nil {
		return clierr.Failure("failed to clear credentials: %w", err)
	}
	fmt.Println("Logged out. All credentials have been cleared.")
	return nil
}

// AuthWhoami prints who the active credential belongs to.
func AuthWhoami() error {
	store, err := auth.LoadEnhancedCredentials()
	if err != nil {
		return clierr.Failure("failed to load credentials: %w", err)
	}

	gatewayURL, err := getGatewayURL()
	if err != nil {
		return err
	}
	creds := store.GetDefaultCredential(gatewayURL)

	if creds == nil || !creds.IsValid() {
		return clierr.Auth("not authenticated: run 'orama auth login'")
	}

	fmt.Println("Authenticated")
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
	return nil
}

// AuthStatus prints the active gateway and credential in detail.
func AuthStatus() error {
	store, err := auth.LoadEnhancedCredentials()
	if err != nil {
		return clierr.Failure("failed to load credentials: %w", err)
	}

	gatewayURL, err := getGatewayURL()
	if err != nil {
		return err
	}
	creds := store.GetDefaultCredential(gatewayURL)

	if env, envErr := GetActiveEnvironment(); envErr == nil {
		fmt.Printf("Active environment: %s\n", env.Name)
	}

	fmt.Println("Authentication status")
	fmt.Printf("  Gateway URL: %s\n", gatewayURL)

	// Not being authenticated is the answer to the question this command asks,
	// not a failure of the command, so it is reported and exits zero.
	if creds == nil {
		fmt.Println("  Status:     not authenticated")
		return nil
	}

	if !creds.IsValid() {
		fmt.Println("  Status:     credentials expired")
		if !creds.ExpiresAt.IsZero() {
			fmt.Printf("  Expired At: %s\n", creds.ExpiresAt.Format("2006-01-02 15:04:05"))
		}
		return nil
	}

	fmt.Println("  Status:     authenticated")
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
	return nil
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
func getGatewayURL() (string, error) {
	url, err := auth.ResolveGatewayURL()
	if err != nil {
		return "", clierr.Usage("%w", err)
	}
	return url, nil
}

// AuthList prints every credential stored for the active gateway.
func AuthList() error {
	store, err := auth.LoadEnhancedCredentials()
	if err != nil {
		return clierr.Failure("failed to load credentials: %w", err)
	}

	gatewayURL, err := getGatewayURL()
	if err != nil {
		return err
	}

	if env, envErr := GetActiveEnvironment(); envErr == nil {
		fmt.Printf("Environment: %s\n", env.Name)
	}
	fmt.Printf("Gateway: %s\n\n", gatewayURL)

	gwCreds := store.Gateways[gatewayURL]
	if gwCreds == nil || len(gwCreds.Credentials) == 0 {
		fmt.Println("No credentials stored for this environment.")
		fmt.Println("Run 'orama auth login' to authenticate.")
		return nil
	}

	fmt.Printf("Stored credentials (%d):\n\n", len(gwCreds.Credentials))
	for i, creds := range gwCreds.Credentials {
		defaultMark := ""
		if i == gwCreds.DefaultIndex {
			defaultMark = "  (active)"
		}

		statusText := "valid"
		if !creds.IsValid() {
			statusText = "expired"
		}

		fmt.Printf("  %d. Wallet: %s%s\n", i+1, creds.Wallet, defaultMark)
		fmt.Printf("     Namespace: %s | Status: %s\n", creds.Namespace, statusText)
		if creds.Plan != "" {
			fmt.Printf("     Plan: %s\n", creds.Plan)
		}
		if !creds.IssuedAt.IsZero() {
			fmt.Printf("     Issued: %s\n", creds.IssuedAt.Format("2006-01-02 15:04:05"))
		}
		fmt.Println()
	}
	return nil
}

// AuthSwitch makes a different stored credential the active one.
func AuthSwitch() error {
	store, err := auth.LoadEnhancedCredentials()
	if err != nil {
		return clierr.Failure("failed to load credentials: %w", err)
	}

	gatewayURL, err := getGatewayURL()
	if err != nil {
		return err
	}

	gwCreds := store.Gateways[gatewayURL]
	if gwCreds == nil || len(gwCreds.Credentials) == 0 {
		return clierr.Auth("no credentials stored for this environment: run 'orama auth login'")
	}

	if len(gwCreds.Credentials) == 1 {
		fmt.Println("Only one credential stored. Nothing to switch to.")
		return nil
	}

	choice, credIndex, err := store.DisplayCredentialMenu(gatewayURL)
	if err != nil {
		return clierr.Failure("menu selection failed: %w", err)
	}

	switch choice {
	case auth.AuthChoiceUseCredential:
		selectedCreds := gwCreds.Credentials[credIndex]
		store.SetDefaultCredential(gatewayURL, credIndex)
		selectedCreds.UpdateLastUsed()
		if err := store.Save(); err != nil {
			return clierr.Failure("failed to save credentials: %w", err)
		}
		fmt.Printf("Switched to wallet: %s\n", selectedCreds.Wallet)
		fmt.Printf("Namespace: %s\n", selectedCreds.Namespace)

	case auth.AuthChoiceAddCredential:
		fmt.Println("Use 'orama auth login' to add a new credential.")

	case auth.AuthChoiceLogout:
		store.ClearAllCredentials()
		if err := store.Save(); err != nil {
			return clierr.Failure("failed to clear credentials: %w", err)
		}
		fmt.Println("All credentials cleared")

	case auth.AuthChoiceExit:
		return clierr.Aborted("cancelled")
	}
	return nil
}
