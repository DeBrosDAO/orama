package auth

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/rwagent"
	"github.com/DeBrosOfficial/network/pkg/tlsutil"
)

// IsRootWalletInstalled checks if the rootwallet agent is reachable.
func IsRootWalletInstalled() bool {
	client := rwagent.New(os.Getenv("RW_AGENT_SOCK"))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return client.IsRunning(ctx)
}

// getRootWalletAddress gets the EVM address from the rootwallet agent.
func getRootWalletAddress() (string, error) {
	client := rwagent.New(os.Getenv("RW_AGENT_SOCK"))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	data, err := client.GetAddress(ctx, "evm")
	if err != nil {
		return "", fmt.Errorf("failed to get address from rootwallet agent: %w", err)
	}
	if data.Address == "" {
		return "", fmt.Errorf("rootwallet agent returned empty address")
	}
	return data.Address, nil
}

// signWithRootWallet signs a message using the rootwallet agent's EVM key.
// The desktop app may prompt the user for approval.
func signWithRootWallet(message string) (string, error) {
	client := rwagent.New(os.Getenv("RW_AGENT_SOCK"))
	// The agent waits up to its own approval timeout for someone to answer the
	// prompt. A context of exactly that length races it, and the loser is the
	// user: they approve the request and the command has already given up with
	// a context deadline instead of the agent's answer.
	ctx, cancel := context.WithTimeout(context.Background(), rwagent.AgentApprovalTimeout+30*time.Second)
	defer cancel()

	data, err := client.Sign(ctx, message, "evm")
	if err != nil {
		return "", fmt.Errorf("failed to sign with rootwallet agent: %w", err)
	}
	if data.Signature == "" {
		return "", fmt.Errorf("rootwallet agent returned empty signature")
	}
	return data.Signature, nil
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

	if !validateEVMWalletAddress(wallet) {
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

	message, err := requestChallenge(client, gatewayURL, wallet, namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to get challenge: %w", err)
	}

	// 4. Sign the message with RootWallet, byte for byte.
	//
	// The gateway verifies the signature against the text it issued, so
	// anything that alters it — trimming, re-wrapping, re-rendering the same
	// fields — produces a signature over different bytes and a failed login.
	// It is also what the RootWallet dialog shows the user, which is the point:
	// it names the domain, the namespace and the deadline in words.
	fmt.Println("⏳ Signing challenge with RootWallet...")
	signature, err := signWithRootWallet(message)
	if err != nil {
		return nil, fmt.Errorf("failed to sign challenge: %w", err)
	}
	fmt.Println("✅ Challenge signed")

	// 5. Verify signature with gateway
	fmt.Println("⏳ Verifying signature with gateway...")
	creds, err := verifySignature(client, gatewayURL, message, signature, namespace)
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
// requestChallenge asks the gateway for the sign-in message to put in front of
// the user, and returns it verbatim.
func requestChallenge(client *http.Client, gatewayURL, wallet, namespace string) (string, error) {
	reqBody := map[string]string{
		"wallet":     wallet,
		"namespace":  namespace,
		"chain_type": "ETH",
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
		Message   string `json:"message"`
		Nonce     string `json:"nonce"`
		Wallet    string `json:"wallet"`
		Namespace string `json:"namespace"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	if result.Message == "" {
		return "", fmt.Errorf("no sign-in message in challenge response: this gateway is older than " +
			"this CLI and still answers with a bare nonce; upgrade the gateway")
	}

	return result.Message, nil
}

// verifySignature sends POST /v1/auth/verify and returns credentials.
//
// The message is the whole credential: the wallet, the nonce and the namespace
// are read out of it by the gateway, because those are the fields the user saw
// and the signature covers.
func verifySignature(client *http.Client, gatewayURL, message, signature, namespace string) (*Credentials, error) {
	reqBody := map[string]string{
		"message":   message,
		"signature": signature,
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

	creds := &Credentials{
		APIKey:       result.APIKey,
		Namespace:    result.Namespace,
		UserID:       result.Subject,
		Wallet:       result.Subject,
		IssuedAt:     time.Now(),
		NamespaceURL: namespaceGatewayURL(gatewayURL, namespace),
	}
	// The session this login was handed. Both of these were read out of the
	// response and dropped, and the API key was sent as the bearer credential
	// of every request afterwards instead.
	creds.SetSession(result.AccessToken, result.RefreshToken, result.ExpiresIn)

	// If 202, namespace cluster is being provisioned — set poll URL
	if resp.StatusCode == http.StatusAccepted && result.PollURL != "" {
		creds.ProvisioningPollURL = result.PollURL
	}

	// ExpiresAt stays unset: it is the API key's own life, and result.ExpiresIn
	// is the access token's. They are different clocks and conflating them made
	// a fifteen-minute number look like the key's expiry.

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
