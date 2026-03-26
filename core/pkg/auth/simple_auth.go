package auth

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/tlsutil"
)

// PerformSimpleAuthentication performs a simple authentication flow where the user
// provides a wallet address and receives an API key without signature verification.
// Requires an existing valid API key (convenience re-auth only).
func PerformSimpleAuthentication(gatewayURL, wallet, namespace, existingAPIKey string) (*Credentials, error) {
	reader := bufio.NewReader(os.Stdin)

	fmt.Println("\n🔐 Simple Wallet Authentication")
	fmt.Println("================================")

	// Read wallet address (skip prompt if provided via flag)
	if wallet == "" {
		fmt.Print("Enter your wallet address (0x...): ")
		walletInput, err := reader.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("failed to read wallet address: %w", err)
		}
		wallet = strings.TrimSpace(walletInput)
	}

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

	// Read namespace (skip prompt if provided via flag)
	if namespace == "" {
		for {
			fmt.Print("Enter namespace (required): ")
			nsInput, err := reader.ReadString('\n')
			if err != nil {
				return nil, fmt.Errorf("failed to read namespace: %w", err)
			}

			namespace = strings.TrimSpace(nsInput)
			if namespace != "" {
				break
			}
			fmt.Println("⚠️  Namespace cannot be empty. Please enter a namespace.")
		}
	}

	fmt.Printf("\n✅ Wallet: %s\n", wallet)
	fmt.Printf("✅ Namespace: %s\n", namespace)
	fmt.Println("⏳ Requesting API key from gateway...")

	// Request API key from gateway
	apiKey, err := requestAPIKeyFromGateway(gatewayURL, wallet, namespace, existingAPIKey)
	if err != nil {
		return nil, fmt.Errorf("failed to request API key: %w", err)
	}

	// Build namespace gateway URL from the gateway URL
	namespaceURL := ""
	if domain := extractDomainFromURL(gatewayURL); domain != "" {
		if namespace == "default" {
			namespaceURL = fmt.Sprintf("https://%s", domain)
		} else {
			namespaceURL = fmt.Sprintf("https://ns-%s.%s", namespace, domain)
		}
	}

	// Create credentials
	creds := &Credentials{
		APIKey:       apiKey,
		Namespace:    namespace,
		UserID:       wallet,
		Wallet:       wallet,
		IssuedAt:     time.Now(),
		NamespaceURL: namespaceURL,
	}

	fmt.Printf("\n🎉 Authentication successful!\n")
	truncatedKey := creds.APIKey
	if len(truncatedKey) > 8 {
		truncatedKey = truncatedKey[:8] + "..."
	}
	fmt.Printf("📝 API Key: %s\n", truncatedKey)

	return creds, nil
}

// requestAPIKeyFromGateway calls the gateway's simple-key endpoint to generate an API key
// For non-default namespaces, this may trigger cluster provisioning and require polling
func requestAPIKeyFromGateway(gatewayURL, wallet, namespace, existingAPIKey string) (string, error) {
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
	// This uses tlsutil which handles Let's Encrypt staging certificates for *.orama.network
	domain := extractDomainFromURL(gatewayURL)
	client := tlsutil.NewHTTPClientForDomain(30*time.Second, domain)

	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if existingAPIKey != "" {
		req.Header.Set("X-API-Key", existingAPIKey)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to call gateway: %w", err)
	}
	defer resp.Body.Close()

	// Handle 202 Accepted - namespace cluster is being provisioned
	if resp.StatusCode == http.StatusAccepted {
		return handleProvisioningResponse(gatewayURL, client, resp, wallet, namespace, existingAPIKey)
	}

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

// handleProvisioningResponse handles 202 Accepted responses when namespace cluster provisioning is needed
func handleProvisioningResponse(gatewayURL string, client *http.Client, resp *http.Response, wallet, namespace, existingAPIKey string) (string, error) {
	var provResp map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&provResp); err != nil {
		return "", fmt.Errorf("failed to decode provisioning response: %w", err)
	}

	status, _ := provResp["status"].(string)
	pollURL, _ := provResp["poll_url"].(string)
	clusterID, _ := provResp["cluster_id"].(string)
	message, _ := provResp["message"].(string)

	if status != "provisioning" {
		return "", fmt.Errorf("unexpected status: %s", status)
	}

	fmt.Printf("\n🏗️  Provisioning namespace cluster...\n")
	if message != "" {
		fmt.Printf("   %s\n", message)
	}
	if clusterID != "" {
		fmt.Printf("   Cluster ID: %s\n", clusterID)
	}
	fmt.Println()

	// Poll until cluster is ready
	if err := pollProvisioningStatus(gatewayURL, client, pollURL); err != nil {
		return "", err
	}

	// Cluster is ready, retry the API key request
	fmt.Println("\n✅ Namespace cluster ready!")
	fmt.Println("⏳ Retrieving API key...")

	return retryAPIKeyRequest(gatewayURL, client, wallet, namespace, existingAPIKey)
}

// pollProvisioningStatus polls the status endpoint until the cluster is ready
func pollProvisioningStatus(gatewayURL string, client *http.Client, pollURL string) error {
	// Build full poll URL if it's a relative path
	if strings.HasPrefix(pollURL, "/") {
		pollURL = gatewayURL + pollURL
	} else {
		// Validate that absolute poll URLs point to the same gateway domain
		gatewayDomain := extractDomainFromURL(gatewayURL)
		pollDomain := extractDomainFromURL(pollURL)
		if gatewayDomain != pollDomain {
			return fmt.Errorf("poll URL domain mismatch: expected %s, got %s", gatewayDomain, pollDomain)
		}
	}

	maxAttempts := 120 // 10 minutes (5 seconds per poll)
	pollInterval := 5 * time.Second

	spinnerChars := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	spinnerIdx := 0

	for i := 0; i < maxAttempts; i++ {
		// Show progress spinner
		fmt.Printf("\r%s Waiting for cluster... ", spinnerChars[spinnerIdx%len(spinnerChars)])
		spinnerIdx++

		resp, err := client.Get(pollURL)
		if err != nil {
			time.Sleep(pollInterval)
			continue
		}

		var statusResp map[string]interface{}
		if err := json.NewDecoder(resp.Body).Decode(&statusResp); err != nil {
			resp.Body.Close()
			time.Sleep(pollInterval)
			continue
		}
		resp.Body.Close()

		status, _ := statusResp["status"].(string)

		switch status {
		case "ready":
			fmt.Printf("\r✅ Cluster ready!                    \n")
			return nil

		case "failed":
			errMsg, _ := statusResp["error"].(string)
			fmt.Printf("\r❌ Provisioning failed                \n")
			return fmt.Errorf("cluster provisioning failed: %s", errMsg)

		case "provisioning":
			// Show progress details
			rqliteReady, _ := statusResp["rqlite_ready"].(bool)
			olricReady, _ := statusResp["olric_ready"].(bool)
			gatewayReady, _ := statusResp["gateway_ready"].(bool)
			dnsReady, _ := statusResp["dns_ready"].(bool)

			progressStr := ""
			if rqliteReady {
				progressStr += "RQLite✓ "
			}
			if olricReady {
				progressStr += "Olric✓ "
			}
			if gatewayReady {
				progressStr += "Gateway✓ "
			}
			if dnsReady {
				progressStr += "DNS✓"
			}
			if progressStr != "" {
				fmt.Printf("\r%s Provisioning... [%s]", spinnerChars[spinnerIdx%len(spinnerChars)], progressStr)
			}

		default:
			// Unknown status, continue polling
		}

		time.Sleep(pollInterval)
	}

	fmt.Printf("\r⚠️  Timeout waiting for cluster       \n")
	return fmt.Errorf("timeout waiting for namespace cluster provisioning")
}

// retryAPIKeyRequest retries the API key request after cluster provisioning
func retryAPIKeyRequest(gatewayURL string, client *http.Client, wallet, namespace, existingAPIKey string) (string, error) {
	reqBody := map[string]string{
		"wallet":    wallet,
		"namespace": namespace,
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	endpoint := gatewayURL + "/v1/auth/simple-key"

	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if existingAPIKey != "" {
		req.Header.Set("X-API-Key", existingAPIKey)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to call gateway: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusAccepted {
		// Still provisioning? This shouldn't happen but handle gracefully
		return "", fmt.Errorf("cluster still provisioning, please try again")
	}

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

// extractDomainFromURL extracts the hostname from a URL, stripping scheme, port, and path.
func extractDomainFromURL(rawURL string) string {
	// Ensure the URL has a scheme so net/url.Parse works correctly
	if !strings.Contains(rawURL, "://") {
		rawURL = "https://" + rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}
