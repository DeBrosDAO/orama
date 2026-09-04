// Package unlock implements the genesis node unlock command.
//
// When the genesis OramaOS node reboots before enough peers exist for
// Shamir-based LUKS key reconstruction, the operator must manually provide
// the LUKS key. This command reads the encrypted genesis key from the
// node's rootfs, decrypts it with the rootwallet, and sends it to the agent.
package unlock

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/DeBrosOfficial/network/pkg/cli/clierr"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Flags holds parsed command-line flags.
type Flags struct {
	NodeIP  string // WireGuard IP of the OramaOS node
	Genesis bool   // Must be set to confirm genesis unlock
	KeyFile string // Path to the encrypted genesis key file (optional override)
}

// Run processes the unlock command.
func Run(flags *Flags) error {
	if err := flags.validate(); err != nil {
		return clierr.Wrap(clierr.CodeUsage, err)
	}

	if !flags.Genesis {
		return clierr.Usage("--genesis is required to confirm a genesis unlock")
	}

	// Step 1: Read the encrypted genesis key from the node
	fmt.Printf("Fetching encrypted genesis key from %s...\n", flags.NodeIP)
	encKey, err := fetchGenesisKey(flags.NodeIP)
	if err != nil && flags.KeyFile == "" {
		return clierr.Unavailable("could not fetch the genesis key from the node: %w\n"+
			"  Provide the key file directly with --key-file", err)
	}

	if flags.KeyFile != "" {
		data, readErr := os.ReadFile(flags.KeyFile)
		if readErr != nil {
			return clierr.NotFound("could not read the key file: %w", readErr)
		}
		encKey = strings.TrimSpace(string(data))
	}

	// Step 2: Decrypt with rootwallet
	fmt.Println("Decrypting genesis key with rootwallet...")
	luksKey, err := decryptGenesisKey(encKey)
	if err != nil {
		return clierr.Failure("decryption failed: %w", err)
	}

	// Step 3: Send LUKS key to the agent over WireGuard
	fmt.Printf("Sending LUKS key to agent at %s:9998...\n", flags.NodeIP)
	if err := sendUnlockKey(flags.NodeIP, luksKey); err != nil {
		return clierr.Failure("unlock failed: %w", err)
	}

	fmt.Println("Genesis node unlocked successfully.")
	fmt.Println("The node is decrypting and mounting its data partition.")
	return nil
}

// validate checks the flag combination.
func (f *Flags) validate() error {
	if f.NodeIP == "" {
		return fmt.Errorf("--node-ip is required")
	}
	return nil
}

// fetchGenesisKey retrieves the encrypted genesis key from the node.
// The agent serves it at GET /v1/agent/genesis-key (only during genesis unlock mode).
func fetchGenesisKey(nodeIP string) (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://%s:9998/v1/agent/genesis-key", nodeIP))
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		EncryptedKey string `json:"encrypted_key"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("invalid response: %w", err)
	}

	return result.EncryptedKey, nil
}

// decryptGenesisKey decrypts the AES-256-GCM encrypted LUKS key using rootwallet.
// The key was encrypted with: AES-256-GCM(luksKey, HKDF(rootwalletKey, "genesis-luks"))
// For now, we use `rw decrypt` if available, or a local HKDF+AES-GCM implementation.
func decryptGenesisKey(encryptedKey string) ([]byte, error) {
	// Try rw decrypt first
	cmd := exec.Command("rw", "decrypt", encryptedKey, "--purpose", "genesis-luks", "--chain", "evm")
	output, err := cmd.Output()
	if err == nil {
		decoded, decErr := base64.StdEncoding.DecodeString(strings.TrimSpace(string(output)))
		if decErr != nil {
			return nil, fmt.Errorf("failed to decode decrypted key: %w", decErr)
		}
		return decoded, nil
	}

	return nil, fmt.Errorf("rw decrypt failed: %w (is rootwallet installed and initialized?)", err)
}

// sendUnlockKey sends the decrypted LUKS key to the agent's unlock endpoint.
func sendUnlockKey(nodeIP string, luksKey []byte) error {
	body, _ := json.Marshal(map[string]string{
		"key": base64.StdEncoding.EncodeToString(luksKey),
	})

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Post(
		fmt.Sprintf("http://%s:9998/v1/agent/unlock", nodeIP),
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("status %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}
