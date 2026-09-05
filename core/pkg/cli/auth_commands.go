package cli

import (
	"fmt"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/auth"
	"github.com/DeBrosOfficial/network/pkg/cli/clierr"
)

// AuthLogin authenticates with a wallet and stores the credential.
func AuthLogin(namespace string) error {
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

	// RootWallet signs the gateway's challenge without the key leaving it, and
	// is the fast path when it is here. When it is not — a server reached over
	// SSH, a container, CI — the login moves to a machine that does have a
	// wallet, rather than being refused. That refusal is why the documented way
	// onto a server was a permanent key in an environment variable.
	var creds *auth.Credentials
	if auth.IsRootWalletInstalled() {
		creds, err = auth.PerformRootWalletAuthentication(gatewayURL, namespace)
	} else {
		creds, err = deviceLogin(gatewayURL, namespace)
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
	fmt.Printf("  Session saved to: %s\n", credsPath)
	fmt.Printf("  Wallet:    %s\n", creds.Wallet)
	fmt.Printf("  Namespace: %s\n", creds.Namespace)
	if creds.NamespaceURL != "" {
		fmt.Printf("  Namespace URL: %s\n", creds.NamespaceURL)
	}
	return nil
}

// AuthLogout ends the session on the gateway and clears what is stored here.
//
// It used to only do the second half, so logging out of a machine you were
// worried about left everything that machine could do intact.
func AuthLogout(all bool) error {
	return AuthLogoutOnline(all)
}

// AuthWhoami asks the gateway who this credential is.
//
// It used to read ~/.orama/credentials.json, which cannot know that a key has
// been revoked: a credential the gateway refuses was reported as authenticated
// until somebody tried to use it for something.
func AuthWhoami() error {
	return AuthWhoamiOnline()
}

// AuthStatus prints what this machine has stored, without asking anybody.
//
// It is deliberately the local view — "what would this shell send, and to
// where" — and says so, because the question of whether the gateway still
// accepts it is what `orama auth whoami` answers.
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

	fmt.Println("Stored on this machine (run 'orama auth whoami' to ask the gateway)")
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

// deviceLogin signs in from a machine with no wallet on it.
func deviceLogin(gatewayURL, namespace string) (*auth.Credentials, error) {
	login, err := auth.StartDeviceLogin(gatewayURL, namespace)
	if err != nil {
		return nil, err
	}

	fmt.Println("\nThere is no wallet on this machine, so the login moves to one that has it.")
	fmt.Printf("\n    Your code:  %s\n\n", login.UserCode)
	fmt.Println("  On a machine where RootWallet is running, run:")
	fmt.Printf("\n    orama auth approve %s\n\n", login.UserCode)
	if namespace != "" {
		fmt.Printf("  It will sign you in to namespace %q.\n", namespace)
	}
	fmt.Printf("  This code is good for %s. Waiting", auth.DeviceLoginWindow)

	creds, err := auth.PollDeviceLogin(gatewayURL, login, func() { fmt.Print(".") })
	fmt.Println()
	if err != nil {
		return nil, err
	}
	return creds, nil
}

// AuthApprove approves, or refuses, a login waiting on another machine.
func AuthApprove(userCode, namespace string, deny bool) error {
	if strings.TrimSpace(userCode) == "" {
		return clierr.Usage("which login: orama auth approve <code>, the code the other machine printed")
	}

	gatewayURL, err := getGatewayURL()
	if err != nil {
		return err
	}

	// The namespace is part of what is being approved, and it is what the
	// signed message will name — so it has to be settled before signing, not
	// inferred afterwards.
	if strings.TrimSpace(namespace) == "" {
		store, storeErr := auth.LoadEnhancedCredentials()
		if storeErr != nil {
			return clierr.Failure("failed to load credentials: %w", storeErr)
		}
		if creds := store.GetDefaultCredential(gatewayURL); creds != nil {
			namespace = creds.Namespace
		}
	}
	if strings.TrimSpace(namespace) == "" {
		return clierr.Usage("which namespace is this login for: orama auth approve <code> --namespace <name>")
	}

	verb := "Approving"
	if deny {
		verb = "Refusing"
	}
	fmt.Printf("%s a login for namespace %s at %s...\n", verb, namespace, gatewayURL)

	wallet, err := auth.ApproveDeviceLogin(gatewayURL, userCode, namespace, deny)
	if err != nil {
		return clierr.Auth("%w", err)
	}

	if deny {
		fmt.Printf("Refused. The machine waiting on %s has stopped.\n", userCode)
		return nil
	}
	fmt.Printf("Approved as %s. The machine waiting on %s has its session.\n", wallet, userCode)
	return nil
}
