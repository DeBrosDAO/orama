// Package enroll implements the OramaOS node enrollment command.
//
// Flow:
//  1. Operator reads the registration code from the OramaOS node's console
//  2. Operator provides code + invite token to Gateway
//  3. Gateway validates, generates cluster config, pushes to node
//  4. Node configures WireGuard, encrypts data partition, starts services
package enroll

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
)

// Run processes the enroll command.
func Run(flags *Flags) error {
	if err := flags.validate(); err != nil {
		return err
	}

	fmt.Printf("Sending enrollment to Gateway at %s...\n", flags.GatewayURL)

	if err := enrollWithGateway(flags.GatewayURL, flags.Token, flags.Code, flags.NodeIP); err != nil {
		return clierr.Failure("enrollment failed: %w", err)
	}

	fmt.Printf("Node %s enrolled successfully.\n", flags.NodeIP)
	fmt.Printf("The node is now configuring WireGuard and encrypting its data partition.\n")
	fmt.Printf("This may take a few minutes. Check status with: orama node status --env %s\n", flags.Env)
	return nil
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

	resp, err := enrollClient().Do(req)
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
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return fmt.Errorf("the gateway redirected the enrollment (%d to %q); the invite token is not sent anywhere but the https URL given",
			resp.StatusCode, resp.Header.Get("Location"))
	}
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("gateway returned %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// enrollTimeout bounds the enrollment call.
const enrollTimeout = 60 * time.Second

// enrollClient never follows a redirect. The request body carries the invite
// token, and Go replays a body on a 307/308 — to any URL, http included, while
// dropping only the Authorization header on a cross-host hop.
func enrollClient() *http.Client {
	return &http.Client{
		Timeout:       enrollTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}
