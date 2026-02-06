package auth

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// EnhancedCredentialStore manages multiple credentials per gateway
type EnhancedCredentialStore struct {
	Gateways map[string]*GatewayCredentials `json:"gateways"`
	Version  string                         `json:"version"`
}

// GatewayCredentials holds multiple credentials for a single gateway
type GatewayCredentials struct {
	Credentials   []*Credentials `json:"credentials"`
	DefaultIndex  int            `json:"default_index"`
	LastUsedIndex int            `json:"last_used_index"`
}

// AuthChoice represents user's choice during authentication
type AuthChoice int

const (
	AuthChoiceUseCredential AuthChoice = iota
	AuthChoiceAddCredential
	AuthChoiceLogout
	AuthChoiceExit
)

// LoadEnhancedCredentials loads the enhanced credential store, with migration support from legacy v2.0 format
func LoadEnhancedCredentials() (*EnhancedCredentialStore, error) {
	credPath, err := GetCredentialsPath()
	if err != nil {
		return nil, err
	}

	// If file doesn't exist, return empty store
	if _, err := os.Stat(credPath); os.IsNotExist(err) {
		return &EnhancedCredentialStore{
			Gateways: make(map[string]*GatewayCredentials),
			Version:  "2.0",
		}, nil
	}

	data, err := os.ReadFile(credPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read credentials file: %w", err)
	}

	// First, try to parse as the proper enhanced store
	var enhancedStore EnhancedCredentialStore
	if err := json.Unmarshal(data, &enhancedStore); err == nil && enhancedStore.Version == "2.0" {
		// Check if it's already in the enhanced format (has credentials arrays)
		hasCredentialsArrays := true
		for _, gwCreds := range enhancedStore.Gateways {
			if gwCreds == nil || gwCreds.Credentials == nil {
				hasCredentialsArrays = false
				break
			}
		}

		if hasCredentialsArrays {
			// Already in enhanced format, just sanitize indices
			if enhancedStore.Gateways == nil {
				enhancedStore.Gateways = make(map[string]*GatewayCredentials)
			}
			for _, gw := range enhancedStore.Gateways {
				if len(gw.Credentials) == 0 {
					gw.DefaultIndex = 0
					gw.LastUsedIndex = 0
					continue
				}
				if gw.DefaultIndex < 0 || gw.DefaultIndex >= len(gw.Credentials) {
					gw.DefaultIndex = 0
				}
				if gw.LastUsedIndex < 0 || gw.LastUsedIndex >= len(gw.Credentials) {
					gw.LastUsedIndex = gw.DefaultIndex
				}
			}
			return &enhancedStore, nil
		}
	}

	// Parse as legacy format (single credential per gateway) and migrate
	// Supports both v1.0 and v2.0 legacy formats
	var legacyStore struct {
		Gateways map[string]*Credentials `json:"gateways"`
		Version  string                  `json:"version"`
	}

	if err := json.Unmarshal(data, &legacyStore); err != nil {
		return nil, fmt.Errorf("invalid credentials file format: %w", err)
	}

	if legacyStore.Version != "1.0" && legacyStore.Version != "2.0" {
		return nil, fmt.Errorf("unsupported credentials version %q; expected \"1.0\" or \"2.0\"", legacyStore.Version)
	}

	// Convert legacy format to enhanced format
	enhanced := &EnhancedCredentialStore{
		Gateways: make(map[string]*GatewayCredentials),
		Version:  "2.0",
	}

	for gwURL, legacyCred := range legacyStore.Gateways {
		if legacyCred == nil {
			// Create empty gateway entry
			enhanced.Gateways[gwURL] = &GatewayCredentials{
				Credentials:   []*Credentials{},
				DefaultIndex:  0,
				LastUsedIndex: 0,
			}
			continue
		}

		// Only add if it looks like a valid credential (has wallet or api key)
		if legacyCred.Wallet != "" || legacyCred.APIKey != "" {
			enhanced.Gateways[gwURL] = &GatewayCredentials{
				Credentials:   []*Credentials{legacyCred},
				DefaultIndex:  0,
				LastUsedIndex: 0,
			}
		} else {
			// Create empty gateway entry
			enhanced.Gateways[gwURL] = &GatewayCredentials{
				Credentials:   []*Credentials{},
				DefaultIndex:  0,
				LastUsedIndex: 0,
			}
		}
	}

	// Auto-save the migrated format
	if err := enhanced.Save(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to save migrated credentials: %v\n", err)
	}

	return enhanced, nil
}

// Save saves the enhanced credential store
func (store *EnhancedCredentialStore) Save() error {
	credPath, err := GetCredentialsPath()
	if err != nil {
		return err
	}

	if store.Version == "" {
		store.Version = "2.0"
	}

	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal credentials: %w", err)
	}

	return os.WriteFile(credPath, data, 0600)
}

// AddCredential adds a new credential for the gateway
func (store *EnhancedCredentialStore) AddCredential(gatewayURL string, creds *Credentials) {
	if store.Gateways == nil {
		store.Gateways = make(map[string]*GatewayCredentials)
	}

	gatewayCredentials := store.Gateways[gatewayURL]
	if gatewayCredentials == nil {
		gatewayCredentials = &GatewayCredentials{
			Credentials:   []*Credentials{},
			DefaultIndex:  0,
			LastUsedIndex: 0,
		}
		store.Gateways[gatewayURL] = gatewayCredentials
	}

	// Check if credential already exists (by wallet address)
	for i, existing := range gatewayCredentials.Credentials {
		if strings.EqualFold(existing.Wallet, creds.Wallet) {
			// Update existing credential
			gatewayCredentials.Credentials[i] = creds
			return
		}
	}

	// Add new credential
	gatewayCredentials.Credentials = append(gatewayCredentials.Credentials, creds)
}

// GetDefaultCredential returns the default credential for a gateway
func (store *EnhancedCredentialStore) GetDefaultCredential(gatewayURL string) *Credentials {
	gatewayCredentials := store.Gateways[gatewayURL]
	if gatewayCredentials == nil || len(gatewayCredentials.Credentials) == 0 {
		return nil
	}

	// Ensure default index is valid
	if gatewayCredentials.DefaultIndex < 0 || gatewayCredentials.DefaultIndex >= len(gatewayCredentials.Credentials) {
		gatewayCredentials.DefaultIndex = 0
	}

	return gatewayCredentials.Credentials[gatewayCredentials.DefaultIndex]
}

// SetDefaultCredential sets the default credential by index
func (store *EnhancedCredentialStore) SetDefaultCredential(gatewayURL string, index int) bool {
	gatewayCredentials := store.Gateways[gatewayURL]
	if gatewayCredentials == nil || index < 0 || index >= len(gatewayCredentials.Credentials) {
		return false
	}

	gatewayCredentials.DefaultIndex = index
	gatewayCredentials.LastUsedIndex = index
	return true
}

// ClearAllCredentials removes all credentials
func (store *EnhancedCredentialStore) ClearAllCredentials() {
	store.Gateways = make(map[string]*GatewayCredentials)
}

// DisplayCredentialMenu shows the interactive credential selection menu
func (store *EnhancedCredentialStore) DisplayCredentialMenu(gatewayURL string) (AuthChoice, int, error) {
	gatewayCredentials := store.Gateways[gatewayURL]

	if gatewayCredentials == nil || len(gatewayCredentials.Credentials) == 0 {
		fmt.Println("\n🔐 No credentials found. Choose an option:")
		fmt.Println("1. Authenticate with new wallet")
		fmt.Println("2. Exit")
		fmt.Print("Choose (1-2): ")

		choice, err := readUserChoice(2)
		if err != nil {
			return AuthChoiceExit, -1, err
		}

		switch choice {
		case 1:
			return AuthChoiceAddCredential, -1, nil
		case 2:
			return AuthChoiceExit, -1, nil
		default:
			return AuthChoiceExit, -1, fmt.Errorf("invalid choice")
		}
	}

	fmt.Printf("\n🔐 Multiple wallets available for %s:\n", gatewayURL)

	// Display credentials
	for i, creds := range gatewayCredentials.Credentials {
		defaultMark := ""
		if i == gatewayCredentials.DefaultIndex {
			defaultMark = " (default)"
		}

		// Format wallet address for display
		displayAddr := creds.Wallet
		if len(displayAddr) > 10 {
			displayAddr = displayAddr[:6] + "..." + displayAddr[len(displayAddr)-4:]
		}

		statusEmoji := "✅"
		if !creds.IsValid() {
			statusEmoji = "❌"
		}

		planInfo := ""
		if creds.Plan != "" {
			planInfo = fmt.Sprintf(" (%s)", creds.Plan)
		}

		fmt.Printf("%d. %s %s%s%s\n", i+1, statusEmoji, displayAddr, planInfo, defaultMark)
	}

	fmt.Printf("%d. Add new wallet\n", len(gatewayCredentials.Credentials)+1)
	fmt.Printf("%d. Logout (clear all credentials)\n", len(gatewayCredentials.Credentials)+2)
	fmt.Printf("%d. Exit\n", len(gatewayCredentials.Credentials)+3)

	maxChoice := len(gatewayCredentials.Credentials) + 3
	fmt.Printf("Choose (1-%d): ", maxChoice)

	choice, err := readUserChoice(maxChoice)
	if err != nil {
		return AuthChoiceExit, -1, err
	}

	if choice <= len(gatewayCredentials.Credentials) {
		// User selected a credential
		return AuthChoiceUseCredential, choice - 1, nil
	} else if choice == len(gatewayCredentials.Credentials)+1 {
		// Add new credential
		return AuthChoiceAddCredential, -1, nil
	} else if choice == len(gatewayCredentials.Credentials)+2 {
		// Logout
		return AuthChoiceLogout, -1, nil
	} else {
		// Exit
		return AuthChoiceExit, -1, nil
	}
}

// readUserChoice reads and validates user input
func readUserChoice(maxChoice int) (int, error) {
	reader := bufio.NewReader(os.Stdin)
	input, err := reader.ReadString('\n')
	if err != nil {
		return 0, fmt.Errorf("failed to read input: %w", err)
	}

	choiceStr := strings.TrimSpace(input)
	choice, err := strconv.Atoi(choiceStr)
	if err != nil {
		return 0, fmt.Errorf("invalid input: please enter a number")
	}

	if choice < 1 || choice > maxChoice {
		return 0, fmt.Errorf("invalid choice: please enter a number between 1 and %d", maxChoice)
	}

	return choice, nil
}

// GetOrPromptForCredentials handles the complete authentication flow
func GetOrPromptForCredentials(gatewayURL string) (*Credentials, error) {
	store, err := LoadEnhancedCredentials()
	if err != nil {
		return nil, fmt.Errorf("failed to load credential store: %w", err)
	}

	// Check if we have a valid default credential
	defaultCreds := store.GetDefaultCredential(gatewayURL)
	if defaultCreds != nil && defaultCreds.IsValid() {
		// Update last used time
		defaultCreds.UpdateLastUsed()
		if err := store.Save(); err != nil {
			// Log warning but don't fail
			fmt.Fprintf(os.Stderr, "Warning: failed to update last used time: %v\n", err)
		}
		return defaultCreds, nil
	}

	// Need to prompt user for credential selection
	for {
		choice, credIndex, err := store.DisplayCredentialMenu(gatewayURL)
		if err != nil {
			return nil, fmt.Errorf("menu selection failed: %w", err)
		}

		switch choice {
		case AuthChoiceUseCredential:
			gatewayCredentials := store.Gateways[gatewayURL]
			if gatewayCredentials == nil || credIndex < 0 || credIndex >= len(gatewayCredentials.Credentials) {
				fmt.Println("❌ Invalid credential selection")
				continue
			}

			selectedCreds := gatewayCredentials.Credentials[credIndex]
			if !selectedCreds.IsValid() {
				fmt.Println("❌ Selected credentials are invalid or expired")
				continue
			}

			// Update default and last used
			store.SetDefaultCredential(gatewayURL, credIndex)
			selectedCreds.UpdateLastUsed()

			if err := store.Save(); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to save credentials: %v\n", err)
			}

			return selectedCreds, nil

		case AuthChoiceAddCredential:
			fmt.Println("\n🌐 Opening browser for wallet authentication...")
			newCreds, err := PerformWalletAuthentication(gatewayURL)
			if err != nil {
				fmt.Printf("❌ Authentication failed: %v\n", err)
				continue
			}

			// Add the new credential
			store.AddCredential(gatewayURL, newCreds)

			// Set as default if it's the first credential
			gatewayCredentials := store.Gateways[gatewayURL]
			if gatewayCredentials != nil && len(gatewayCredentials.Credentials) == 1 {
				store.SetDefaultCredential(gatewayURL, 0)
			}

			if err := store.Save(); err != nil {
				return nil, fmt.Errorf("failed to save new credentials: %w", err)
			}

			fmt.Printf("✅ Wallet %s added successfully\n", newCreds.Wallet)
			return newCreds, nil

		case AuthChoiceLogout:
			store.ClearAllCredentials()
			if err := store.Save(); err != nil {
				return nil, fmt.Errorf("failed to clear credentials: %w", err)
			}
			fmt.Println("✅ All credentials cleared")
			continue

		case AuthChoiceExit:
			return nil, fmt.Errorf("authentication cancelled by user")

		default:
			fmt.Println("❌ Invalid choice")
			continue
		}
	}
}

// HasValidEnhancedCredentials checks if there are valid credentials for the default gateway
func HasValidEnhancedCredentials() (bool, error) {
	store, err := LoadEnhancedCredentials()
	if err != nil {
		return false, err
	}

	gatewayURL := GetDefaultGatewayURL()
	defaultCreds := store.GetDefaultCredential(gatewayURL)

	return defaultCreds != nil && defaultCreds.IsValid(), nil
}

// GetValidEnhancedCredentials returns valid credentials for the default gateway
func GetValidEnhancedCredentials() (*Credentials, error) {
	store, err := LoadEnhancedCredentials()
	if err != nil {
		return nil, err
	}

	gatewayURL := GetDefaultGatewayURL()
	defaultCreds := store.GetDefaultCredential(gatewayURL)

	if defaultCreds == nil {
		return nil, fmt.Errorf("no credentials found for gateway %s", gatewayURL)
	}

	if !defaultCreds.IsValid() {
		return nil, fmt.Errorf("credentials for gateway %s are expired or invalid", gatewayURL)
	}

	return defaultCreds, nil
}
