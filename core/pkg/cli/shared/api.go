package shared

import (
	"fmt"
	"os"

	"github.com/DeBrosOfficial/network/pkg/auth"
)

// GetAPIURL returns the gateway/API URL from env var or active environment config.
func GetAPIURL() string {
	if url := os.Getenv("ORAMA_API_URL"); url != "" {
		return url
	}
	return auth.GetDefaultGatewayURL()
}

// GetAuthToken returns an auth token from env var or the credentials store.
func GetAuthToken() (string, error) {
	if token := os.Getenv("ORAMA_TOKEN"); token != "" {
		return token, nil
	}

	store, err := auth.LoadEnhancedCredentials()
	if err != nil {
		return "", fmt.Errorf("failed to load credentials: %w", err)
	}

	gatewayURL := auth.GetDefaultGatewayURL()
	creds := store.GetDefaultCredential(gatewayURL)
	if creds == nil {
		return "", fmt.Errorf("no credentials found for %s. Run 'orama auth login' to authenticate", gatewayURL)
	}

	if !creds.IsValid() {
		return "", fmt.Errorf("credentials expired for %s. Run 'orama auth login' to re-authenticate", gatewayURL)
	}

	return creds.APIKey, nil
}
