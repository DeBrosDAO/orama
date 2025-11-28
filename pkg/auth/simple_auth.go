package auth

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/tlsutil"
)

// PerformSimpleAuthentication performs a simple authentication flow where the user
// provides a wallet address and receives an API key without signature verification
func PerformSimpleAuthentication(gatewayURL string) (*Credentials, error) {
	reader := bufio.NewReader(os.Stdin)

	fmt.Println("\n🔐 Simple Wallet Authentication")
	fmt.Println("================================")

	// Read wallet address
	fmt.Print("Enter your wallet address (0x...): ")
	walletInput, err := reader.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("failed to read wallet address: %w", err)
	}

	wallet := strings.TrimSpace(walletInput)
	if wallet == "" {
		return nil, fmt.Errorf("wallet address cannot be empty")
	}

	// Validate wallet format (basic check)
	if !strings.HasPrefix(wallet, "0x") && !strings.HasPrefix(wallet, "0X") {
		wallet = "0x" + wallet
	}

	if !ValidateWalletAddress(wallet) {
		return nil, fmt.Errorf("invalid wallet address format")
	}

	// Read namespace (optional)
	fmt.Print("Enter namespace (press Enter for 'default'): ")
	nsInput, err := reader.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("failed to read namespace: %w", err)
	}

	namespace := strings.TrimSpace(nsInput)
	if namespace == "" {
		namespace = "default"
	}

	fmt.Printf("\n✅ Wallet: %s\n", wallet)
	fmt.Printf("✅ Namespace: %s\n", namespace)
	fmt.Println("⏳ Requesting API key from gateway...")

	// Request API key from gateway
	apiKey, err := requestAPIKeyFromGateway(gatewayURL, wallet, namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to request API key: %w", err)
	}

	// Create credentials
	creds := &Credentials{
		APIKey:    apiKey,
		Namespace: namespace,
		UserID:    wallet,
		Wallet:    wallet,
		IssuedAt:  time.Now(),
	}

	fmt.Printf("\n🎉 Authentication successful!\n")
	fmt.Printf("📝 API Key: %s\n", creds.APIKey)

	return creds, nil
}

// requestAPIKeyFromGateway calls the gateway's simple-key endpoint to generate an API key
func requestAPIKeyFromGateway(gatewayURL, wallet, namespace string) (string, error) {
	reqBody := map[string]string{
		"wallet":    wallet,
		"namespace": namespace,
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	endpoint := gatewayURL + "/v1/auth/simple-key"

	// Extract domain from URL for TLS configuration
	// This uses tlsutil which handles Let's Encrypt staging certificates for *.debros.network
	domain := extractDomainFromURL(gatewayURL)
	client := tlsutil.NewHTTPClientForDomain(30*time.Second, domain)

	resp, err := client.Post(endpoint, "application/json", bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("failed to call gateway: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("gateway returned status %d: %s", resp.StatusCode, string(body))
	}

	var respBody map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&respBody); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	apiKey, ok := respBody["api_key"].(string)
	if !ok || apiKey == "" {
		return "", fmt.Errorf("no api_key in response")
	}

	return apiKey, nil
}

// extractDomainFromURL extracts the domain from a URL
// Removes protocol (https://, http://), path, and port components
func extractDomainFromURL(url string) string {
	// Remove protocol prefixes
	url = strings.TrimPrefix(url, "https://")
	url = strings.TrimPrefix(url, "http://")

	// Remove path component
	if idx := strings.Index(url, "/"); idx != -1 {
		url = url[:idx]
	}

	// Remove port component
	if idx := strings.Index(url, ":"); idx != -1 {
		url = url[:idx]
	}

	return url
}
