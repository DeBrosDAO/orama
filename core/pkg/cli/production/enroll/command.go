// Package enroll implements the OramaOS node enrollment command.
//
// Flow:
//  1. Operator fetches registration code from the OramaOS node (port 9999)
//  2. Operator provides code + invite token to Gateway
//  3. Gateway validates, generates cluster config, pushes to node
//  4. Node configures WireGuard, encrypts data partition, starts services
package enroll

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/DeBrosOfficial/network/pkg/cli/clierr"
	"io"
	"net/http"
	"time"
)

// Handle processes the enroll command.
func Run(flags *Flags) error {
	if err := flags.validate(); err != nil {
		return err
	}

	// Step 1: Fetch registration code from the OramaOS node
	fmt.Printf("Fetching registration code from %s:9999...\n", flags.NodeIP)

	var code string
	if flags.Code != "" {
		// Code provided directly — skip fetch
		code = flags.Code
	} else {
		fetchedCode, err := fetchRegistrationCode(flags.NodeIP)
		if err != nil {
			return clierr.Unavailable("could not reach the OramaOS node: %w\n"+
				"  Make sure the node is booted and port 9999 is reachable", err)
		}
		code = fetchedCode
	}

	fmt.Printf("Registration code: %s\n", code)

	// Step 2: Send enrollment request to the Gateway
	fmt.Printf("Sending enrollment to Gateway at %s...\n", flags.GatewayURL)

	if err := enrollWithGateway(flags.GatewayURL, flags.Token, code, flags.NodeIP); err != nil {
		return clierr.Failure("enrollment failed: %w", err)
	}

	fmt.Printf("Node %s enrolled successfully.\n", flags.NodeIP)
	fmt.Printf("The node is now configuring WireGuard and encrypting its data partition.\n")
	fmt.Printf("This may take a few minutes. Check status with: orama node status --env %s\n", flags.Env)
	return nil
}

// fetchRegistrationCode retrieves the one-time registration code from the OramaOS node.
func fetchRegistrationCode(nodeIP string) (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://%s:9999/", nodeIP))
	if err != nil {
		return "", fmt.Errorf("GET failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusGone {
		return "", fmt.Errorf("registration code already served (node may be partially enrolled)")
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	var result struct {
		Code    string `json:"code"`
		Expires string `json:"expires"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("invalid response: %w", err)
	}

	return result.Code, nil
}

// enrollWithGateway sends the enrollment request to the Gateway, which validates
// the code and token, then pushes cluster configuration to the OramaOS node.
func enrollWithGateway(gatewayURL, token, code, nodeIP string) error {
	body, _ := json.Marshal(map[string]string{
		"code":    code,
		"token":   token,
		"node_ip": nodeIP,
	})

	req, err := http.NewRequest("POST", gatewayURL+"/v1/node/enroll", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("invalid or expired invite token")
	}
	if resp.StatusCode == http.StatusBadRequest {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("bad request: %s", string(respBody))
	}
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("gateway returned %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}
