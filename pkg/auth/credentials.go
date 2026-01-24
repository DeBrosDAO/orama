package auth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Credentials represents authentication credentials for a specific gateway
type Credentials struct {
	APIKey       string    `json:"api_key"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	Namespace    string    `json:"namespace"`
	UserID       string    `json:"user_id,omitempty"`
	Wallet       string    `json:"wallet,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
	IssuedAt     time.Time `json:"issued_at"`
	LastUsedAt   time.Time `json:"last_used_at,omitempty"`
	Plan         string    `json:"plan,omitempty"`
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

// GetDefaultGatewayURL returns the default gateway URL from environment config, env vars, or fallback
func GetDefaultGatewayURL() string {
	// Check environment variables first (for backwards compatibility)
	if envURL := os.Getenv("DEBROS_GATEWAY_URL"); envURL != "" {
		return envURL
	}
	if envURL := os.Getenv("DEBROS_GATEWAY"); envURL != "" {
		return envURL
	}

	// Try to read from environment config file
	if gwURL := getGatewayFromEnvConfig(); gwURL != "" {
		return gwURL
	}

	return "http://localhost:6001"
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

	gatewayURL := GetDefaultGatewayURL()
	creds, exists := store.GetCredentialsForGateway(gatewayURL)

	return exists && creds.IsValid(), nil
}

// SaveCredentialsForDefaultGateway saves credentials for the default gateway
func SaveCredentialsForDefaultGateway(creds *Credentials) error {
	store, err := LoadCredentials()
	if err != nil {
		return err
	}

	gatewayURL := GetDefaultGatewayURL()
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
