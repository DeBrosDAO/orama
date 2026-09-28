package globalbind

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// KeyFile is a service secret the bind command reads. It never writes it back.
type KeyFile struct {
	// Kind is secp256k1, ed25519, or ed25519-expanded.
	Kind   string
	Secret []byte
}

// ReadKeyFile reads a raw secret, hex, or a CometBFT priv_key JSON file.
// CometBFT ed25519 secrets are seed||pubkey; only the seed is kept.
func ReadKeyFile(path string) (KeyFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return KeyFile{}, fmt.Errorf("read the service key: %w", err)
	}
	text := strings.TrimSpace(string(data))
	if strings.HasPrefix(text, "{") {
		return parseCometKey([]byte(text))
	}
	if decoded, err := hex.DecodeString(text); err == nil && (len(decoded) == 32 || len(decoded) == 64) {
		return kindFromLength(decoded), nil
	}
	return kindFromLength(data), nil
}

func kindFromLength(secret []byte) KeyFile {
	switch len(secret) {
	case 64:
		return KeyFile{Kind: KeyTypeEd25519Expanded, Secret: secret}
	default:
		return KeyFile{Kind: KeyTypeSecp256k1, Secret: secret}
	}
}

func parseCometKey(data []byte) (KeyFile, error) {
	var doc struct {
		PrivKey struct {
			Type  string `json:"type"`
			Value string `json:"value"`
		} `json:"priv_key"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return KeyFile{}, fmt.Errorf("service key is not JSON")
	}
	raw, err := base64.StdEncoding.DecodeString(doc.PrivKey.Value)
	if err != nil {
		return KeyFile{}, fmt.Errorf("service key value is not base64")
	}
	switch doc.PrivKey.Type {
	case "tendermint/PrivKeyEd25519":
		if len(raw) != 64 {
			return KeyFile{}, fmt.Errorf("cometbft ed25519 key is %d bytes, want 64", len(raw))
		}
		return KeyFile{Kind: KeyTypeEd25519, Secret: raw[:32]}, nil
	case "tendermint/PrivKeySecp256k1":
		if len(raw) != 32 {
			return KeyFile{}, fmt.Errorf("cometbft secp256k1 key is %d bytes, want 32", len(raw))
		}
		return KeyFile{Kind: KeyTypeSecp256k1, Secret: raw}, nil
	default:
		return KeyFile{}, fmt.Errorf("unsupported cometbft key type %q", doc.PrivKey.Type)
	}
}

// SignKey signs a binding with a key file. A 64-byte raw file is an expanded
// ed25519 secret; pass kind ed25519 only through CometBFT JSON, whose 64-byte
// value is seed||pubkey.
func SignKey(key KeyFile, chainID, operator, service string) (Binding, error) {
	switch key.Kind {
	case KeyTypeSecp256k1:
		return SignSecp256k1(key.Secret, chainID, operator, service)
	case KeyTypeEd25519:
		return SignEd25519(key.Secret, chainID, operator, service)
	case KeyTypeEd25519Expanded:
		return SignExpandedEd25519(key.Secret, chainID, operator, service)
	default:
		return Binding{}, fmt.Errorf("unknown key type %q", key.Kind)
	}
}
