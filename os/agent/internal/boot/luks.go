package boot

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/DeBrosOfficial/orama-os/agent/internal/types"
	"github.com/DeBrosOfficial/orama-os/agent/internal/wireguard"
)

// GenerateLUKSKey generates a cryptographically random 32-byte key for LUKS encryption.
func GenerateLUKSKey() ([]byte, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("failed to read random bytes: %w", err)
	}
	return key, nil
}

// FormatAndEncrypt formats a device with LUKS2 encryption and creates an ext4 filesystem.
func FormatAndEncrypt(device string, key []byte) error {
	log.Printf("formatting %s with LUKS2", device)

	// cryptsetup luksFormat --type luks2 --cipher aes-xts-plain64 <device> --key-file=-
	cmd := exec.Command("cryptsetup", "luksFormat", "--type", "luks2",
		"--cipher", "aes-xts-plain64", "--batch-mode", device, "--key-file=-")
	cmd.Stdin = bytes.NewReader(key)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("luksFormat failed: %w\n%s", err, string(output))
	}

	// cryptsetup open <device> orama-data --key-file=-
	cmd = exec.Command("cryptsetup", "open", device, DataMapperName, "--key-file=-")
	cmd.Stdin = bytes.NewReader(key)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("cryptsetup open failed: %w\n%s", err, string(output))
	}

	// mkfs.ext4 /dev/mapper/orama-data
	cmd = exec.Command("mkfs.ext4", "-F", "/dev/mapper/"+DataMapperName)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("mkfs.ext4 failed: %w\n%s", err, string(output))
	}

	// Mount
	if err := os.MkdirAll(DataMountPoint, 0755); err != nil {
		return fmt.Errorf("failed to create mount point: %w", err)
	}
	cmd = exec.Command("mount", "/dev/mapper/"+DataMapperName, DataMountPoint)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("mount failed: %w\n%s", err, string(output))
	}

	log.Println("LUKS partition formatted and mounted")
	return nil
}

// DecryptAndMount opens and mounts an existing LUKS partition.
func DecryptAndMount(device string, key []byte) error {
	// cryptsetup open <device> orama-data --key-file=-
	cmd := exec.Command("cryptsetup", "open", device, DataMapperName, "--key-file=-")
	cmd.Stdin = bytes.NewReader(key)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("cryptsetup open failed: %w\n%s", err, string(output))
	}

	if err := os.MkdirAll(DataMountPoint, 0755); err != nil {
		return fmt.Errorf("failed to create mount point: %w", err)
	}

	cmd = exec.Command("mount", "/dev/mapper/"+DataMapperName, DataMountPoint)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("mount failed: %w\n%s", err, string(output))
	}

	return nil
}

// DistributeKeyShares splits the LUKS key into Shamir shares and pushes them
// to peer vault-guardians over WireGuard.
func DistributeKeyShares(key []byte, peers []types.Peer, nodeID string) error {
	n := len(peers)
	if n == 0 {
		return fmt.Errorf("no peers available for key distribution")
	}

	k := adaptiveThreshold(n)

	log.Printf("splitting LUKS key into %d shares (threshold=%d)", n, k)

	shares, err := shamirSplit(key, n, k)
	if err != nil {
		return fmt.Errorf("shamir split failed: %w", err)
	}

	id, err := loadOrCreateVaultIdentity()
	if err != nil {
		return fmt.Errorf("failed to load vault identity: %w", err)
	}

	secretName := fmt.Sprintf("luks-key-%s", nodeID)
	for i := range peers {
		session, err := vaultAuth(peers[i].WGIP, id)
		if err != nil {
			return fmt.Errorf("failed to authenticate with peer %d: %w", i+1, err)
		}

		shareB64 := base64.StdEncoding.EncodeToString(shares[i])
		if err := vaultPutSecret(peers[i].WGIP, session, id, secretName, shareB64, 1); err != nil {
			return fmt.Errorf("failed to store share on peer %d: %w", i+1, err)
		}

		log.Printf("stored share %d/%d", i+1, n)
	}

	return nil
}

// FetchAndReconstruct fetches Shamir shares from peers and reconstructs the LUKS key.
// Uses exponential backoff: 1s, 2s, 4s, 8s, 16s, max 5 retries.
func FetchAndReconstruct(wg *wireguard.Manager) ([]byte, error) {
	peers, err := loadPeerConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load peer config: %w", err)
	}

	nodeID, err := loadNodeID()
	if err != nil {
		return nil, fmt.Errorf("failed to load node ID: %w", err)
	}

	id, err := loadOrCreateVaultIdentity()
	if err != nil {
		return nil, fmt.Errorf("failed to load vault identity: %w", err)
	}

	n := len(peers)
	k := adaptiveThreshold(n)

	secretName := fmt.Sprintf("luks-key-%s", nodeID)

	var shares [][]byte
	const maxRetries = 5

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			delay := time.Duration(1<<uint(attempt-1)) * time.Second
			log.Printf("retrying share fetch in %v (attempt %d/%d)", delay, attempt, maxRetries)
			time.Sleep(delay)
		}

		shares = nil
		for i, peer := range peers {
			session, authErr := vaultAuth(peer.WGIP, id)
			if authErr != nil {
				log.Printf("auth failed with peer %d: %v", i+1, authErr)
				continue
			}

			shareB64, getErr := vaultGetSecret(peer.WGIP, session, id, secretName)
			if getErr != nil {
				log.Printf("share fetch failed from peer %d: %v", i+1, getErr)
				continue
			}

			shareBytes, decErr := base64.StdEncoding.DecodeString(shareB64)
			if decErr != nil {
				log.Printf("invalid share from peer %d: %v", i+1, decErr)
				continue
			}

			shares = append(shares, shareBytes)
			if len(shares) >= k+1 { // fetch K+1 for malicious share detection
				break
			}
		}

		if len(shares) >= k {
			break
		}
	}

	if len(shares) < k {
		return nil, fmt.Errorf("could not fetch enough shares: got %d, need %d", len(shares), k)
	}

	// Reconstruct key
	key, err := shamirCombine(shares[:k])
	if err != nil {
		return nil, fmt.Errorf("shamir combine failed: %w", err)
	}

	// If we have K+1 shares, the two windows must reconstruct the same key.
	// A mismatch is a failed unlock, not a warning that still returns a key.
	if len(shares) > k {
		altKey, altErr := shamirCombine(shares[1 : k+1])
		if altErr != nil || !bytes.Equal(key, altKey) {
			ZeroBytes(key)
			ZeroBytes(altKey)
			if altErr != nil {
				return nil, fmt.Errorf("share sets disagree: %w", altErr)
			}
			return nil, fmt.Errorf("share sets disagree")
		}
		ZeroBytes(altKey)
	}

	return key, nil
}

// ZeroBytes overwrites a byte slice with zeros to clear sensitive data from memory.
func ZeroBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
	runtime.KeepAlive(b)
}

func adaptiveThreshold(n int) int {
	if n <= 0 {
		return 0
	}
	k := n / 3
	if k < 2 {
		k = 2
	}
	if k > n {
		k = n
	}
	return k
}

// vaultAuth authenticates with a peer's vault-guardian using the V2 challenge-response flow.
// Returns a session token valid for 1 hour. The session is not an ownership proof;
// PUT/GET still sign with the vault identity key.
func vaultAuth(peerIP string, id *vaultIdentity) (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}

	challengeBody, _ := json.Marshal(map[string]string{"identity": id.identity})
	resp, err := client.Post(
		fmt.Sprintf("http://%s:10106/v2/vault/auth/challenge", peerIP),
		"application/json",
		bytes.NewReader(challengeBody),
	)
	if err != nil {
		return "", fmt.Errorf("challenge request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("challenge returned status %d", resp.StatusCode)
	}

	var challengeResp struct {
		Nonce     string `json:"nonce"`
		CreatedNs int64  `json:"created_ns"`
		Tag       string `json:"tag"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&challengeResp); err != nil {
		return "", fmt.Errorf("failed to parse challenge response: %w", err)
	}

	sessionBody, _ := json.Marshal(map[string]interface{}{
		"identity":   id.identity,
		"nonce":      challengeResp.Nonce,
		"created_ns": challengeResp.CreatedNs,
		"tag":        challengeResp.Tag,
	})
	resp2, err := client.Post(
		fmt.Sprintf("http://%s:10106/v2/vault/auth/session", peerIP),
		"application/json",
		bytes.NewReader(sessionBody),
	)
	if err != nil {
		return "", fmt.Errorf("session request failed: %w", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		return "", fmt.Errorf("session returned status %d", resp2.StatusCode)
	}

	var sessionResp struct {
		Identity string `json:"identity"`
		ExpiryNs int64  `json:"expiry_ns"`
		Tag      string `json:"tag"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&sessionResp); err != nil {
		return "", fmt.Errorf("failed to parse session response: %w", err)
	}

	return fmt.Sprintf("%s:%d:%s", id.identity, sessionResp.ExpiryNs, sessionResp.Tag), nil
}

// vaultPutSecret stores a secret via the V2 vault API (PUT).
func vaultPutSecret(peerIP, sessionToken string, id *vaultIdentity, name, value string, version int) error {
	client := &http.Client{Timeout: 10 * time.Second}
	body, _ := json.Marshal(map[string]interface{}{
		"share":   value,
		"version": version,
	})

	req, err := http.NewRequest("PUT",
		fmt.Sprintf("http://%s:10106/v2/vault/secrets/%s", peerIP, name),
		bytes.NewReader(body))
	if err != nil {
		return err
	}
	msg := fmt.Sprintf("vault-secret-put-v1:%s:%s:%d", id.identity, name, version)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Session-Token", sessionToken)
	req.Header.Set("X-Vault-Pubkey", id.pubHex)
	req.Header.Set("X-Vault-Signature", id.sign(msg))

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("PUT request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("vault PUT returned %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// vaultGetSecret retrieves a secret via the V2 vault API (GET).
func vaultGetSecret(peerIP, sessionToken string, id *vaultIdentity, name string) (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}

	req, err := http.NewRequest("GET",
		fmt.Sprintf("http://%s:10106/v2/vault/secrets/%s", peerIP, name), nil)
	if err != nil {
		return "", err
	}
	ts := time.Now().Unix()
	msg := fmt.Sprintf("vault-secret-get-v1:%s:%s:%d", id.identity, name, ts)
	req.Header.Set("X-Session-Token", sessionToken)
	req.Header.Set("X-Vault-Pubkey", id.pubHex)
	req.Header.Set("X-Vault-Signature", id.sign(msg))
	req.Header.Set("X-Vault-Timestamp", fmt.Sprintf("%d", ts))

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("GET request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("vault GET returned %d", resp.StatusCode)
	}

	var result struct {
		Share string `json:"share"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to parse vault response: %w", err)
	}

	return result.Share, nil
}

// shamirSplit splits a secret into n shares with threshold k.
// Uses Shamir's Secret Sharing over GF(256).
func shamirSplit(secret []byte, n, k int) ([][]byte, error) {
	if n < k {
		return nil, fmt.Errorf("n (%d) must be >= k (%d)", n, k)
	}
	if k < 2 {
		return nil, fmt.Errorf("threshold must be >= 2")
	}

	shares := make([][]byte, n)
	for i := range shares {
		shares[i] = make([]byte, 1+len(secret))
		shares[i][0] = byte(i + 1) // x-coordinate travels with the share
	}

	// For each byte of the secret, create a random polynomial of degree k-1
	for byteIdx := 0; byteIdx < len(secret); byteIdx++ {
		coeffs := make([]byte, k)
		coeffs[0] = secret[byteIdx]
		if _, err := rand.Read(coeffs[1:]); err != nil {
			return nil, err
		}

		for i := 0; i < n; i++ {
			shares[i][1+byteIdx] = evalPolynomial(coeffs, shares[i][0])
		}
		ZeroBytes(coeffs)
	}

	return shares, nil
}

// shamirCombine reconstructs a secret from k shares using Lagrange interpolation over GF(256).
func shamirCombine(shares [][]byte) ([]byte, error) {
	if len(shares) < 2 {
		return nil, fmt.Errorf("need at least 2 shares")
	}

	if len(shares[0]) < 2 {
		return nil, fmt.Errorf("share too short")
	}
	secretLen := len(shares[0]) - 1
	secret := make([]byte, secretLen)

	xs := make([]byte, len(shares))
	ys := make([][]byte, len(shares))
	seen := make(map[byte]struct{}, len(shares))
	for i, share := range shares {
		if len(share) != len(shares[0]) {
			return nil, fmt.Errorf("share length mismatch")
		}
		x := share[0]
		if x == 0 {
			return nil, fmt.Errorf("share x-coordinate must not be 0")
		}
		if _, dup := seen[x]; dup {
			return nil, fmt.Errorf("duplicate share x-coordinate")
		}
		seen[x] = struct{}{}
		xs[i] = x
		ys[i] = share[1:]
	}

	for byteIdx := 0; byteIdx < secretLen; byteIdx++ {
		// Lagrange interpolation at x=0
		var val byte
		for i, xi := range xs {
			// Compute Lagrange basis polynomial L_i(0)
			num := byte(1)
			den := byte(1)
			for j, xj := range xs {
				if i == j {
					continue
				}
				num = gf256Mul(num, xj)    // 0 - xj = xj in GF(256) (additive inverse = self)
				den = gf256Mul(den, xi^xj) // xi - xj = xi XOR xj
			}
			lagrange := gf256Mul(num, gf256Inv(den))
			val ^= gf256Mul(ys[i][byteIdx], lagrange)
		}
		secret[byteIdx] = val
	}

	return secret, nil
}

// evalPolynomial evaluates a polynomial at x over GF(256).
func evalPolynomial(coeffs []byte, x byte) byte {
	result := coeffs[len(coeffs)-1]
	for i := len(coeffs) - 2; i >= 0; i-- {
		result = gf256Mul(result, x) ^ coeffs[i]
	}
	return result
}

// GF(256) multiplication using the AES (Rijndael) irreducible polynomial: x^8 + x^4 + x^3 + x + 1
func gf256Mul(a, b byte) byte {
	var result byte
	for b > 0 {
		if b&1 != 0 {
			result ^= a
		}
		hi := a & 0x80
		a <<= 1
		if hi != 0 {
			a ^= 0x1B // x^8 + x^4 + x^3 + x + 1
		}
		b >>= 1
	}
	return result
}

// gf256Inv computes the multiplicative inverse in GF(256) using extended Euclidean or lookup.
// Uses Fermat's little theorem: a^(-1) = a^(254) in GF(256).
func gf256Inv(a byte) byte {
	if a == 0 {
		return 0 // 0 has no inverse, but we return 0 by convention
	}
	result := a
	for i := 0; i < 6; i++ {
		result = gf256Mul(result, result)
		result = gf256Mul(result, a)
	}
	result = gf256Mul(result, result) // now result = a^254
	return result
}

// loadPeerConfig loads the peer list from the enrollment config.
func loadPeerConfig() ([]types.Peer, error) {
	data, err := os.ReadFile(filepath.Join(OramaDir, "configs", "peers.json"))
	if err != nil {
		return nil, err
	}
	var peers []types.Peer
	if err := json.Unmarshal(data, &peers); err != nil {
		return nil, err
	}
	return peers, nil
}

// loadNodeID loads this node's ID from the enrollment config.
func loadNodeID() (string, error) {
	data, err := os.ReadFile(filepath.Join(OramaDir, "configs", "node-id"))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}
