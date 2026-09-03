package boot

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
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

	// Adaptive threshold: at least 3, or n/3 (whichever is greater)
	k := int(math.Max(3, float64(n)/3.0))
	if k > n {
		k = n
	}

	log.Printf("splitting LUKS key into %d shares (threshold=%d)", n, k)

	shares, err := shamirSplit(key, n, k)
	if err != nil {
		return fmt.Errorf("shamir split failed: %w", err)
	}

	// Derive agent identity from the node's WG private key
	identity, err := deriveAgentIdentity()
	if err != nil {
		return fmt.Errorf("failed to derive agent identity: %w", err)
	}

	for i, peer := range peers {
		session, err := vaultAuth(peer.WGIP, identity)
		if err != nil {
			return fmt.Errorf("failed to authenticate with peer %s: %w", peer.WGIP, err)
		}

		shareB64 := base64.StdEncoding.EncodeToString(shares[i])
		secretName := fmt.Sprintf("luks-key-%s", nodeID)

		if err := vaultPutSecret(peer.WGIP, session, secretName, shareB64, 1); err != nil {
			return fmt.Errorf("failed to store share on peer %s: %w", peer.WGIP, err)
		}

		log.Printf("stored share %d/%d on peer %s", i+1, n, peer.WGIP)
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

	identity, err := deriveAgentIdentity()
	if err != nil {
		return nil, fmt.Errorf("failed to derive agent identity: %w", err)
	}

	n := len(peers)
	k := int(math.Max(3, float64(n)/3.0))
	if k > n {
		k = n
	}

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
		for _, peer := range peers {
			session, authErr := vaultAuth(peer.WGIP, identity)
			if authErr != nil {
				log.Printf("auth failed with peer %s: %v", peer.WGIP, authErr)
				continue
			}

			shareB64, getErr := vaultGetSecret(peer.WGIP, session, secretName)
			if getErr != nil {
				log.Printf("share fetch failed from peer %s: %v", peer.WGIP, getErr)
				continue
			}

			shareBytes, decErr := base64.StdEncoding.DecodeString(shareB64)
			if decErr != nil {
				log.Printf("invalid share from peer %s: %v", peer.WGIP, decErr)
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

	// If we have K+1 shares, verify consistency (malicious share detection)
	if len(shares) > k {
		altKey, altErr := shamirCombine(shares[1 : k+1])
		if altErr == nil && !bytes.Equal(key, altKey) {
			ZeroBytes(altKey)
			log.Println("WARNING: malicious share detected — share sets produce different keys")
			// TODO: identify the bad share, alert cluster, exclude that peer
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
}

// deriveAgentIdentity derives a deterministic identity from the WG private key.
func deriveAgentIdentity() (string, error) {
	data, err := os.ReadFile("/etc/wireguard/private.key")
	if err != nil {
		return "", fmt.Errorf("failed to read WG private key: %w", err)
	}
	hash := sha256.Sum256(bytes.TrimSpace(data))
	return hex.EncodeToString(hash[:]), nil
}

// vaultAuth authenticates with a peer's vault-guardian using the V2 challenge-response flow.
// Returns a session token valid for 1 hour.
func vaultAuth(peerIP, identity string) (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}

	// Step 1: Request challenge
	challengeBody, _ := json.Marshal(map[string]string{"identity": identity})
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
		Nonce string `json:"nonce"`
		Tag   string `json:"tag"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&challengeResp); err != nil {
		return "", fmt.Errorf("failed to parse challenge response: %w", err)
	}

	// Step 2: Create session
	sessionBody, _ := json.Marshal(map[string]string{
		"identity": identity,
		"nonce":    challengeResp.Nonce,
		"tag":      challengeResp.Tag,
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
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&sessionResp); err != nil {
		return "", fmt.Errorf("failed to parse session response: %w", err)
	}

	return sessionResp.Token, nil
}

// vaultPutSecret stores a secret via the V2 vault API (PUT).
func vaultPutSecret(peerIP, sessionToken, name, value string, version int) error {
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
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Session-Token", sessionToken)

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
func vaultGetSecret(peerIP, sessionToken, name string) (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}

	req, err := http.NewRequest("GET",
		fmt.Sprintf("http://%s:10106/v2/vault/secrets/%s", peerIP, name), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("X-Session-Token", sessionToken)

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
		shares[i] = make([]byte, len(secret))
	}

	// For each byte of the secret, create a random polynomial of degree k-1
	for byteIdx := 0; byteIdx < len(secret); byteIdx++ {
		// Generate random coefficients for the polynomial
		// coeffs[0] = secret byte, coeffs[1..k-1] = random
		coeffs := make([]byte, k)
		coeffs[0] = secret[byteIdx]
		if _, err := rand.Read(coeffs[1:]); err != nil {
			return nil, err
		}

		// Evaluate polynomial at points 1, 2, ..., n
		for i := 0; i < n; i++ {
			x := byte(i + 1) // x = 1, 2, ..., n (never 0)
			shares[i][byteIdx] = evalPolynomial(coeffs, x)
		}
	}

	return shares, nil
}

// shamirCombine reconstructs a secret from k shares using Lagrange interpolation over GF(256).
func shamirCombine(shares [][]byte) ([]byte, error) {
	if len(shares) < 2 {
		return nil, fmt.Errorf("need at least 2 shares")
	}

	secretLen := len(shares[0])
	secret := make([]byte, secretLen)

	// Share indices are 1-based (x = 1, 2, 3, ...)
	// We need to know which x values we have
	xs := make([]byte, len(shares))
	for i := range xs {
		xs[i] = byte(i + 1)
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
				num = gf256Mul(num, xj)           // 0 - xj = xj in GF(256) (additive inverse = self)
				den = gf256Mul(den, xi^xj)         // xi - xj = xi XOR xj
			}
			lagrange := gf256Mul(num, gf256Inv(den))
			val ^= gf256Mul(shares[i][byteIdx], lagrange)
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

