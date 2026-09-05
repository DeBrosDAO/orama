package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Credentials represents authentication credentials for a specific gateway
type Credentials struct {
	APIKey       string `json:"api_key"`
	RefreshToken string `json:"refresh_token,omitempty"`
	// AccessToken is the short-lived credential every request carries. Login
	// has always returned one; it used to be read out of the response and
	// dropped, and the API key was sent instead.
	AccessToken string `json:"access_token,omitempty"`
	// AccessTokenExpiresAt is when it stops being worth sending. It is not
	// ExpiresAt below, which is the credential's own life.
	AccessTokenExpiresAt time.Time `json:"access_token_expires_at,omitempty"`
	Namespace            string    `json:"namespace"`
	UserID               string    `json:"user_id,omitempty"`
	Wallet               string    `json:"wallet,omitempty"`
	ExpiresAt            time.Time `json:"expires_at,omitempty"`
	IssuedAt             time.Time `json:"issued_at"`
	LastUsedAt           time.Time `json:"last_used_at,omitempty"`
	Plan                 string    `json:"plan,omitempty"`
	NamespaceURL         string    `json:"namespace_url,omitempty"`

	// ProvisioningPollURL is set when namespace cluster is being provisioned.
	// Used only during the login flow, not persisted.
	ProvisioningPollURL string `json:"-"`
}

// CredentialStore manages credentials for multiple gateways
type CredentialStore struct {
	Gateways map[string]*Credentials `json:"gateways"`
	Version  string                  `json:"version"`
}

// GetCredentialsPath returns the path to the credentials file
func GetCredentialsPath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}

	oramaDir := filepath.Join(homeDir, ".orama")
	if err := os.MkdirAll(oramaDir, 0700); err != nil {
		return "", fmt.Errorf("failed to create .orama directory: %w", err)
	}

	return filepath.Join(oramaDir, "credentials.json"), nil
}

// LoadCredentials loads credentials from ~/.orama/credentials.json
func LoadCredentials() (*CredentialStore, error) {
	credPath, err := GetCredentialsPath()
	if err != nil {
		return nil, err
	}

	// If file doesn't exist, return empty store
	if _, err := os.Stat(credPath); os.IsNotExist(err) {
		return &CredentialStore{
			Gateways: make(map[string]*Credentials),
			Version:  "1.0",
		}, nil
	}

	data, err := os.ReadFile(credPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read credentials file: %w", err)
	}

	var store CredentialStore
	if err := json.Unmarshal(data, &store); err != nil {
		return nil, fmt.Errorf("failed to parse credentials file: %w", err)
	}

	// Initialize gateways map if nil
	if store.Gateways == nil {
		store.Gateways = make(map[string]*Credentials)
	}

	// Set version if empty
	if store.Version == "" {
		store.Version = "1.0"
	}

	return &store, nil
}

// SaveCredentials saves credentials to ~/.orama/credentials.json
func (store *CredentialStore) SaveCredentials() error {
	credPath, err := GetCredentialsPath()
	if err != nil {
		return err
	}

	// Ensure version is set
	if store.Version == "" {
		store.Version = "1.0"
	}

	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal credentials: %w", err)
	}

	// Write with restricted permissions (readable only by owner)
	if err := os.WriteFile(credPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write credentials file: %w", err)
	}

	return nil
}

// GetCredentialsForGateway returns credentials for a specific gateway URL
func (store *CredentialStore) GetCredentialsForGateway(gatewayURL string) (*Credentials, bool) {
	creds, exists := store.Gateways[gatewayURL]
	if !exists || creds == nil {
		return nil, false
	}

	// Check if credentials are expired (if expiration is set)
	if !creds.ExpiresAt.IsZero() && time.Now().After(creds.ExpiresAt) {
		return nil, false
	}

	return creds, true
}

// SetCredentialsForGateway stores credentials for a specific gateway URL
func (store *CredentialStore) SetCredentialsForGateway(gatewayURL string, creds *Credentials) {
	if store.Gateways == nil {
		store.Gateways = make(map[string]*Credentials)
	}

	// Update last used time
	creds.LastUsedAt = time.Now()

	store.Gateways[gatewayURL] = creds
}

// RemoveCredentialsForGateway removes credentials for a specific gateway URL
func (store *CredentialStore) RemoveCredentialsForGateway(gatewayURL string) {
	if store.Gateways != nil {
		delete(store.Gateways, gatewayURL)
	}
}

// IsExpired checks if credentials are expired
func (creds *Credentials) IsExpired() bool {
	if creds.ExpiresAt.IsZero() {
		return false // No expiration set
	}
	return time.Now().After(creds.ExpiresAt)
}

// IsValid checks if credentials are valid (not empty and not expired)
func (creds *Credentials) IsValid() bool {
	if creds == nil {
		return false
	}

	if creds.APIKey == "" {
		return false
	}

	return !creds.IsExpired()
}

// UpdateLastUsed updates the last used timestamp
func (creds *Credentials) UpdateLastUsed() {
	creds.LastUsedAt = time.Now()
}

// ErrNoGateway means no gateway is configured for this shell.
var ErrNoGateway = errors.New("no gateway configured: set ORAMA_API_URL, or run 'orama env add <name> <url>' and 'orama env use <name>'")

// gatewayEnvVars are the environment variables that name a gateway, in the
// order they are consulted. All three exist for historical reasons; keeping
// them in one list is what stops different commands from honouring different
// subsets, which is how a request could be sent to one gateway with the
// credential stored for another.
var gatewayEnvVars = []string{"ORAMA_API_URL", "ORAMA_GATEWAY_URL", "ORAMA_GATEWAY"}

// ResolveGatewayURL returns the gateway this shell talks to.
//
// Precedence: environment variable, then the active environment in
// ~/.orama/environments.json. There is deliberately no built-in default —
// silently falling back to a hardcoded network meant a misconfigured shell
// quietly talked to devnet, and credentials were then looked up for a gateway
// the caller never asked for.
//
// Every caller that needs a credential must key it on the URL this returns.
func ResolveGatewayURL() (string, error) {
	for _, name := range gatewayEnvVars {
		if url := strings.TrimSpace(os.Getenv(name)); url != "" {
			return url, nil
		}
	}

	if gwURL := getGatewayFromEnvConfig(); gwURL != "" {
		return gwURL, nil
	}

	return "", ErrNoGateway
}

// getGatewayFromEnvConfig reads the active environment's gateway URL from the config file
func getGatewayFromEnvConfig() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	envConfigPath := filepath.Join(homeDir, ".orama", "environments.json")
	data, err := os.ReadFile(envConfigPath)
	if err != nil {
		return ""
	}

	var config struct {
		Environments []struct {
			Name       string `json:"name"`
			GatewayURL string `json:"gateway_url"`
		} `json:"environments"`
		ActiveEnvironment string `json:"active_environment"`
	}

	if err := json.Unmarshal(data, &config); err != nil {
		return ""
	}

	// Find the active environment
	for _, env := range config.Environments {
		if env.Name == config.ActiveEnvironment {
			return env.GatewayURL
		}
	}

	return ""
}

// HasValidCredentials checks if there are valid credentials for the default gateway
func HasValidCredentials() (bool, error) {
	store, err := LoadCredentials()
	if err != nil {
		return false, err
	}

	gatewayURL, err := ResolveGatewayURL()
	if err != nil {
		return false, err
	}
	creds, exists := store.GetCredentialsForGateway(gatewayURL)

	return exists && creds.IsValid(), nil
}

// SaveCredentialsForDefaultGateway saves credentials for the default gateway
func SaveCredentialsForDefaultGateway(creds *Credentials) error {
	store, err := LoadCredentials()
	if err != nil {
		return err
	}

	gatewayURL, err := ResolveGatewayURL()
	if err != nil {
		return err
	}
	store.SetCredentialsForGateway(gatewayURL, creds)

	return store.SaveCredentials()
}

// ClearAllCredentials removes all stored credentials
func ClearAllCredentials() error {
	store := &CredentialStore{
		Gateways: make(map[string]*Credentials),
		Version:  "1.0",
	}

	return store.SaveCredentials()
}
