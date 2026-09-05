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

	// Step 1: read the encrypted genesis key.
	//
	// This used to try GET /v1/agent/genesis-key on the node first. The
	// OramaOS agent serves /v1/agent/unlock, /v1/agent/command, /status,
	// /health and /logs, and has never served that path — so the fetch always
	// failed, and every run reached the "provide --key-file" fallback after a
	// ten-second timeout. Requiring the flag says so up front.
	data, err := os.ReadFile(flags.KeyFile)
	if err != nil {
		return clierr.NotFound("could not read the key file: %w", err)
	}
	encKey := strings.TrimSpace(string(data))
	if encKey == "" {
		return clierr.Usage("%s is empty; it must hold the encrypted genesis key", flags.KeyFile)
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
	if f.KeyFile == "" {
		return fmt.Errorf("--key-file is required: the encrypted genesis key is written " +
			"where the node was created, and the OramaOS agent does not serve it")
	}
	return nil
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
