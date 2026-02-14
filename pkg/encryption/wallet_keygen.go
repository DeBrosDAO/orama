package encryption

import (
	"crypto/ed25519"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/hkdf"
)

// NodeKeys holds all cryptographic keys derived from a wallet's master key.
type NodeKeys struct {
	LibP2PPrivateKey  ed25519.PrivateKey // Ed25519 for LibP2P identity
	LibP2PPublicKey   ed25519.PublicKey
	WireGuardKey      [32]byte // Curve25519 private key (clamped)
	WireGuardPubKey   [32]byte // Curve25519 public key
	IPFSPrivateKey    ed25519.PrivateKey
	IPFSPublicKey     ed25519.PublicKey
	ClusterPrivateKey ed25519.PrivateKey // IPFS Cluster identity
	ClusterPublicKey  ed25519.PublicKey
	JWTPrivateKey     ed25519.PrivateKey // EdDSA JWT signing key
	JWTPublicKey      ed25519.PublicKey
}

// DeriveNodeKeysFromWallet calls `rw derive` to get a master key from the user's
// Root Wallet, then expands it into all node keys. The wallet's private key never
// leaves the `rw` process.
//
// vpsIP is used as the HKDF info parameter, so each VPS gets unique keys from the
// same wallet. Stdin is passed through so rw can prompt for the wallet password.
func DeriveNodeKeysFromWallet(vpsIP string) (*NodeKeys, error) {
	if vpsIP == "" {
		return nil, fmt.Errorf("VPS IP is required for key derivation")
	}

	// Check rw is installed
	if _, err := exec.LookPath("rw"); err != nil {
		return nil, fmt.Errorf("Root Wallet (rw) not found in PATH — install it first")
	}

	// Call rw derive to get master key bytes
	cmd := exec.Command("rw", "derive", "--salt", "orama-node", "--info", vpsIP)
	cmd.Stdin = os.Stdin   // pass through for password prompts
	cmd.Stderr = os.Stderr // rw UI messages go to terminal
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("rw derive failed: %w", err)
	}

	masterHex := strings.TrimSpace(string(out))
	if len(masterHex) != 64 { // 32 bytes = 64 hex chars
		return nil, fmt.Errorf("rw derive returned unexpected output length: %d (expected 64 hex chars)", len(masterHex))
	}

	masterKey, err := hexToBytes(masterHex)
	if err != nil {
		return nil, fmt.Errorf("rw derive returned invalid hex: %w", err)
	}
	defer zeroBytes(masterKey)

	return ExpandNodeKeys(masterKey)
}

// ExpandNodeKeys expands a 32-byte master key into all node keys using HKDF-SHA256.
// The master key should come from `rw derive --salt "orama-node" --info "<IP>"`.
//
// Each key type uses a different HKDF info string under the salt "orama-expand",
// ensuring cryptographic independence between key types.
func ExpandNodeKeys(masterKey []byte) (*NodeKeys, error) {
	if len(masterKey) != 32 {
		return nil, fmt.Errorf("master key must be 32 bytes, got %d", len(masterKey))
	}

	salt := []byte("orama-expand")
	keys := &NodeKeys{}

	// Derive LibP2P Ed25519 key
	seed, err := deriveBytes(masterKey, salt, []byte("libp2p-identity"), ed25519.SeedSize)
	if err != nil {
		return nil, fmt.Errorf("failed to derive libp2p key: %w", err)
	}
	priv := ed25519.NewKeyFromSeed(seed)
	zeroBytes(seed)
	keys.LibP2PPrivateKey = priv
	keys.LibP2PPublicKey = priv.Public().(ed25519.PublicKey)

	// Derive WireGuard Curve25519 key
	wgSeed, err := deriveBytes(masterKey, salt, []byte("wireguard-key"), 32)
	if err != nil {
		return nil, fmt.Errorf("failed to derive wireguard key: %w", err)
	}
	copy(keys.WireGuardKey[:], wgSeed)
	zeroBytes(wgSeed)
	clampCurve25519Key(&keys.WireGuardKey)
	pubKey, err := curve25519.X25519(keys.WireGuardKey[:], curve25519.Basepoint)
	if err != nil {
		return nil, fmt.Errorf("failed to compute wireguard public key: %w", err)
	}
	copy(keys.WireGuardPubKey[:], pubKey)

	// Derive IPFS Ed25519 key
	seed, err = deriveBytes(masterKey, salt, []byte("ipfs-identity"), ed25519.SeedSize)
	if err != nil {
		return nil, fmt.Errorf("failed to derive ipfs key: %w", err)
	}
	priv = ed25519.NewKeyFromSeed(seed)
	zeroBytes(seed)
	keys.IPFSPrivateKey = priv
	keys.IPFSPublicKey = priv.Public().(ed25519.PublicKey)

	// Derive IPFS Cluster Ed25519 key
	seed, err = deriveBytes(masterKey, salt, []byte("ipfs-cluster"), ed25519.SeedSize)
	if err != nil {
		return nil, fmt.Errorf("failed to derive cluster key: %w", err)
	}
	priv = ed25519.NewKeyFromSeed(seed)
	zeroBytes(seed)
	keys.ClusterPrivateKey = priv
	keys.ClusterPublicKey = priv.Public().(ed25519.PublicKey)

	// Derive JWT EdDSA signing key
	seed, err = deriveBytes(masterKey, salt, []byte("jwt-signing"), ed25519.SeedSize)
	if err != nil {
		return nil, fmt.Errorf("failed to derive jwt key: %w", err)
	}
	priv = ed25519.NewKeyFromSeed(seed)
	zeroBytes(seed)
	keys.JWTPrivateKey = priv
	keys.JWTPublicKey = priv.Public().(ed25519.PublicKey)

	return keys, nil
}

// deriveBytes uses HKDF-SHA256 to derive n bytes from the given IKM, salt, and info.
func deriveBytes(ikm, salt, info []byte, n int) ([]byte, error) {
	hkdfReader := hkdf.New(sha256.New, ikm, salt, info)
	out := make([]byte, n)
	if _, err := io.ReadFull(hkdfReader, out); err != nil {
		return nil, err
	}
	return out, nil
}

// clampCurve25519Key applies the standard Curve25519 clamping to a private key.
func clampCurve25519Key(key *[32]byte) {
	key[0] &= 248
	key[31] &= 127
	key[31] |= 64
}

// hexToBytes decodes a hex string to bytes.
func hexToBytes(hex string) ([]byte, error) {
	if len(hex)%2 != 0 {
		return nil, fmt.Errorf("odd-length hex string")
	}
	b := make([]byte, len(hex)/2)
	for i := 0; i < len(hex); i += 2 {
		var hi, lo byte
		var err error
		if hi, err = hexCharToByte(hex[i]); err != nil {
			return nil, err
		}
		if lo, err = hexCharToByte(hex[i+1]); err != nil {
			return nil, err
		}
		b[i/2] = hi<<4 | lo
	}
	return b, nil
}

func hexCharToByte(c byte) (byte, error) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', nil
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, nil
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, nil
	default:
		return 0, fmt.Errorf("invalid hex character: %c", c)
	}
}

// zeroBytes zeroes a byte slice to clear sensitive data from memory.
func zeroBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
