package auth

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/tlsutil"
)

// IsRootWalletInstalled checks if the `rw` CLI is available in PATH
func IsRootWalletInstalled() bool {
	_, err := exec.LookPath("rw")
	return err == nil
}

// getRootWalletAddress gets the EVM address from the RootWallet keystore
func getRootWalletAddress() (string, error) {
	cmd := exec.Command("rw", "address", "--chain", "evm")
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get address from rw: %w", err)
	}
	addr := strings.TrimSpace(string(out))
	if addr == "" {
		return "", fmt.Errorf("rw returned empty address — run 'rw init' first")
	}
	return addr, nil
}

// signWithRootWallet signs a message using RootWallet's EVM key.
// Stdin is passed through so the user can enter their password if the session is expired.
func signWithRootWallet(message string) (string, error) {
	cmd := exec.Command("rw", "sign", message, "--chain", "evm")
	cmd.Stdin = os.Stdin
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to sign with rw: %w", err)
	}
	sig := strings.TrimSpace(string(out))
	if sig == "" {
		return "", fmt.Errorf("rw returned empty signature")
	}
	return sig, nil
}

// PerformRootWalletAuthentication performs a challenge-response authentication flow
// using the RootWallet CLI to sign a gateway-issued nonce
func PerformRootWalletAuthentication(gatewayURL, namespace string) (*Credentials, error) {
	reader := bufio.NewReader(os.Stdin)

	fmt.Println("\n🔐 RootWallet Authentication")
	fmt.Println("=============================")

	// 1. Get wallet address from RootWallet
	fmt.Println("⏳ Reading wallet address from RootWallet...")
	wallet, err := getRootWalletAddress()
	if err != nil {
		return nil, fmt.Errorf("failed to get wallet address: %w", err)
	}

	if !ValidateWalletAddress(wallet) {
		return nil, fmt.Errorf("invalid wallet address from rw: %s", wallet)
	}

	fmt.Printf("✅ Wallet: %s\n", wallet)

	// 2. Prompt for namespace if not provided
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
	fmt.Printf("✅ Namespace: %s\n", namespace)

	// 3. Request challenge nonce from gateway
	fmt.Println("⏳ Requesting authentication challenge...")
	domain := extractDomainFromURL(gatewayURL)
	client := tlsutil.NewHTTPClientForDomain(30*time.Second, domain)

	nonce, err := requestChallenge(client, gatewayURL, wallet, namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to get challenge: %w", err)
	}

	// 4. Sign the nonce with RootWallet
	fmt.Println("⏳ Signing challenge with RootWallet...")
	signature, err := signWithRootWallet(nonce)
	if err != nil {
		return nil, fmt.Errorf("failed to sign challenge: %w", err)
	}
	fmt.Println("✅ Challenge signed")

	// 5. Verify signature with gateway
	fmt.Println("⏳ Verifying signature with gateway...")
	creds, err := verifySignature(client, gatewayURL, wallet, nonce, signature, namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to verify signature: %w", err)
	}

	// If namespace cluster is being provisioned, poll until ready
	if creds.ProvisioningPollURL != "" {
		fmt.Println("⏳ Provisioning namespace cluster...")
		pollErr := pollNamespaceProvisioning(client, gatewayURL, creds.ProvisioningPollURL)
		if pollErr != nil {
			fmt.Printf("⚠️  Provisioning poll failed: %v\n", pollErr)
			fmt.Println("   Credentials are saved. Cluster may still be provisioning in background.")
		} else {
			fmt.Println("✅ Namespace cluster ready!")
		}
	}

	fmt.Printf("\n🎉 Authentication successful!\n")
	fmt.Printf("🏢 Namespace: %s\n", creds.Namespace)

	return creds, nil
}

// requestChallenge sends POST /v1/auth/challenge and returns the nonce
func requestChallenge(client *http.Client, gatewayURL, wallet, namespace string) (string, error) {
	reqBody := map[string]string{
		"wallet":    wallet,
		"namespace": namespace,
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	resp, err := client.Post(gatewayURL+"/v1/auth/challenge", "application/json", bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("failed to call gateway: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("gateway returned status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Nonce     string `json:"nonce"`
		Wallet    string `json:"wallet"`
		Namespace string `json:"namespace"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	if result.Nonce == "" {
		return "", fmt.Errorf("no nonce in challenge response")
	}

	return result.Nonce, nil
}

// verifySignature sends POST /v1/auth/verify and returns credentials
func verifySignature(client *http.Client, gatewayURL, wallet, nonce, signature, namespace string) (*Credentials, error) {
	reqBody := map[string]string{
		"wallet":     wallet,
		"nonce":      nonce,
		"signature":  signature,
		"namespace":  namespace,
		"chain_type": "ETH",
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	resp, err := client.Post(gatewayURL+"/v1/auth/verify", "application/json", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("failed to call gateway: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("gateway returned status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		Subject      string `json:"subject"`
		Namespace    string `json:"namespace"`
		APIKey       string `json:"api_key"`
		// Provisioning fields (202 Accepted)
		Status  string `json:"status"`
		PollURL string `json:"poll_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if result.APIKey == "" {
		return nil, fmt.Errorf("no api_key in verify response")
	}

	// Build namespace gateway URL
	namespaceURL := ""
	if d := extractDomainFromURL(gatewayURL); d != "" {
		if namespace == "default" {
			namespaceURL = fmt.Sprintf("https://%s", d)
		} else {
			namespaceURL = fmt.Sprintf("https://ns-%s.%s", namespace, d)
		}
	}

	creds := &Credentials{
		APIKey:       result.APIKey,
		RefreshToken: result.RefreshToken,
		Namespace:    result.Namespace,
		UserID:       result.Subject,
		Wallet:       result.Subject,
		IssuedAt:     time.Now(),
		NamespaceURL: namespaceURL,
	}

	// If 202, namespace cluster is being provisioned — set poll URL
	if resp.StatusCode == http.StatusAccepted && result.PollURL != "" {
		creds.ProvisioningPollURL = result.PollURL
	}

	// Note: result.ExpiresIn is the JWT access token lifetime (15min),
	// NOT the API key lifetime. Don't set ExpiresAt — the API key is permanent.

	return creds, nil
}

// pollNamespaceProvisioning polls the namespace status endpoint until the cluster is ready.
func pollNamespaceProvisioning(client *http.Client, gatewayURL, pollPath string) error {
	pollURL := gatewayURL + pollPath
	timeout := time.After(120 * time.Second)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			return fmt.Errorf("timed out after 120s waiting for namespace cluster")
		case <-ticker.C:
			resp, err := client.Get(pollURL)
			if err != nil {
				continue // Retry on network error
			}

			var status struct {
				Status string `json:"status"`
			}
			decErr := json.NewDecoder(resp.Body).Decode(&status)
			resp.Body.Close()
			if decErr != nil {
				continue
			}

			switch status.Status {
			case "ready":
				return nil
			case "failed", "error":
				return fmt.Errorf("namespace provisioning failed")
			}
			// "provisioning" or other — keep polling
			fmt.Print(".")
		}
	}
}
