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
	qrterminal "github.com/mdp/qrterminal/v3"
)

// Hardcoded Phantom auth React app URL (deployed on Orama devnet)
const phantomAuthURL = "https://phantom-auth-y0w9aa.orama-devnet.network"

// PhantomSession represents a phantom auth session from the gateway.
type PhantomSession struct {
	SessionID string `json:"session_id"`
	ExpiresAt string `json:"expires_at"`
}

// PhantomSessionStatus represents the polled status of a phantom auth session.
type PhantomSessionStatus struct {
	SessionID string `json:"session_id"`
	Status    string `json:"status"`
	Wallet    string `json:"wallet"`
	APIKey    string `json:"api_key"`
	Namespace string `json:"namespace"`
	Error     string `json:"error"`
}

// PerformPhantomAuthentication runs the Phantom Solana auth flow:
// 1. Prompt for namespace
// 2. Create session via gateway
// 3. Display QR code in terminal
// 4. Poll for completion
// 5. Return credentials
func PerformPhantomAuthentication(gatewayURL, namespace string) (*Credentials, error) {
	reader := bufio.NewReader(os.Stdin)

	fmt.Println("\n🟣 Phantom Wallet Authentication (Solana)")
	fmt.Println("==========================================")
	fmt.Println("Requires an NFT from the authorized collection.")

	// Prompt for namespace if empty
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
			fmt.Println("Namespace cannot be empty.")
		}
	}

	domain := extractDomainFromURL(gatewayURL)
	client := tlsutil.NewHTTPClientForDomain(30*time.Second, domain)

	// 1. Create phantom session
	fmt.Println("\nCreating authentication session...")
	session, err := createPhantomSession(client, gatewayURL, namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to create session: %w", err)
	}

	// 2. Build auth URL and display QR code
	authURL := fmt.Sprintf("%s/?session=%s&gateway=%s&namespace=%s",
		phantomAuthURL, session.SessionID, url.QueryEscape(gatewayURL), url.QueryEscape(namespace))

	fmt.Println("\nScan this QR code with your phone to authenticate:")
	fmt.Println()
	qrterminal.GenerateWithConfig(authURL, qrterminal.Config{
		Level:     qrterminal.M,
		Writer:    os.Stdout,
		BlackChar: qrterminal.BLACK,
		WhiteChar: qrterminal.WHITE,
		QuietZone: 1,
	})
	fmt.Println()
	fmt.Printf("Or open this URL on your phone:\n%s\n\n", authURL)
	fmt.Println("Waiting for authentication... (timeout: 5 minutes)")

	// 3. Poll for completion
	creds, err := pollPhantomSession(client, gatewayURL, session.SessionID)
	if err != nil {
		return nil, err
	}

	// Set namespace and build namespace URL
	creds.Namespace = namespace
	if domain := extractDomainFromURL(gatewayURL); domain != "" {
		if namespace == "default" {
			creds.NamespaceURL = fmt.Sprintf("https://%s", domain)
		} else {
			creds.NamespaceURL = fmt.Sprintf("https://ns-%s.%s", namespace, domain)
		}
	}

	fmt.Printf("\n🎉 Authentication successful!\n")
	truncatedKey := creds.APIKey
	if len(truncatedKey) > 8 {
		truncatedKey = truncatedKey[:8] + "..."
	}
	fmt.Printf("📝 API Key: %s\n", truncatedKey)

	return creds, nil
}

// createPhantomSession creates a new phantom auth session via the gateway.
func createPhantomSession(client *http.Client, gatewayURL, namespace string) (*PhantomSession, error) {
	reqBody := map[string]string{
		"namespace": namespace,
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	resp, err := client.Post(gatewayURL+"/v1/auth/phantom/session", "application/json", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("failed to call gateway: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("gateway returned status %d: %s", resp.StatusCode, string(body))
	}

	var session PhantomSession
	if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &session, nil
}

// pollPhantomSession polls the gateway for session completion.
func pollPhantomSession(client *http.Client, gatewayURL, sessionID string) (*Credentials, error) {
	pollInterval := 2 * time.Second
	maxDuration := 5 * time.Minute
	deadline := time.Now().Add(maxDuration)

	spinnerChars := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	spinnerIdx := 0

	for time.Now().Before(deadline) {
		resp, err := client.Get(gatewayURL + "/v1/auth/phantom/session/" + sessionID)
		if err != nil {
			time.Sleep(pollInterval)
			continue
		}

		var status PhantomSessionStatus
		if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
			resp.Body.Close()
			time.Sleep(pollInterval)
			continue
		}
		resp.Body.Close()

		switch status.Status {
		case "completed":
			fmt.Printf("\r✅ Authenticated!                    \n")
			return &Credentials{
				APIKey:   status.APIKey,
				Wallet:   status.Wallet,
				UserID:   status.Wallet,
				IssuedAt: time.Now(),
			}, nil

		case "failed":
			fmt.Printf("\r❌ Authentication failed              \n")
			errMsg := status.Error
			if errMsg == "" {
				errMsg = "unknown error"
			}
			return nil, fmt.Errorf("authentication failed: %s", errMsg)

		case "expired":
			fmt.Printf("\r⏰ Session expired                   \n")
			return nil, fmt.Errorf("authentication session expired")

		case "pending":
			fmt.Printf("\r%s Waiting for phone authentication... ", spinnerChars[spinnerIdx%len(spinnerChars)])
			spinnerIdx++
		}

		time.Sleep(pollInterval)
	}

	fmt.Printf("\r⏰ Timeout                           \n")
	return nil, fmt.Errorf("authentication timed out after 5 minutes")
}
