package report

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
)

// The chain section reaches the public status page, and whatever answers on
// the RPC's loopback port writes it. A value outside the shape CometBFT v0.39
// itself produces is refused, never shown.
var (
	// chainValidatorAddressRe is a validator address as CometBFT encodes it:
	// bytes.HexBytes, whose JSON is the upper-case hex of 20 bytes.
	chainValidatorAddressRe = regexp.MustCompile(`^[0-9A-F]{40}$`)
	// chainIDRe is a conservative chain id (node_info.network).
	chainIDRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
	// chainNodeVersionRe is a CometBFT version string (node_info.version).
	chainNodeVersionRe = regexp.MustCompile(`^[A-Za-z0-9.+_-]{1,64}$`)
)

const (
	// chainNodeKeyType is the amino type name of CometBFT's ed25519 private key.
	chainNodeKeyType = "tendermint/PrivKeyEd25519"
	// chainNodeIDBytes is the length of a CometBFT address (crypto.AddressSize):
	// SHA-256 truncated to 20 bytes.
	chainNodeIDBytes = 20
)

// chainNodeKeyFile is CometBFT's node_key.json. The value is the base64 of the
// 64-byte ed25519 private key: the seed, then the public key.
type chainNodeKeyFile struct {
	PrivKey struct {
		Type  string `json:"type"`
		Value []byte `json:"value"`
	} `json:"priv_key"`
}

// chainNodeID derives this node's CometBFT p2p id from its node key exactly as
// CometBFT v0.39 does (p2p.PubKeyToID): the lower-case hex of the first 20
// bytes of SHA-256 over the ed25519 public key, which CometBFT takes from the
// private key's last 32 bytes (ed25519.PrivKey.PubKey).
func chainNodeID(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read the chain node key: %w", err)
	}
	var key chainNodeKeyFile
	if err := json.Unmarshal(data, &key); err != nil {
		return "", fmt.Errorf("decode the chain node key %s: %w", path, err)
	}
	if key.PrivKey.Type != chainNodeKeyType {
		return "", fmt.Errorf("the chain node key %s is not a %s", path, chainNodeKeyType)
	}
	if len(key.PrivKey.Value) != ed25519.PrivateKeySize {
		return "", fmt.Errorf("the chain node key %s is %d bytes, want %d",
			path, len(key.PrivKey.Value), ed25519.PrivateKeySize)
	}
	pub := key.PrivKey.Value[ed25519.SeedSize:]
	if bytes.Equal(pub, make([]byte, ed25519.PublicKeySize)) {
		return "", fmt.Errorf("the chain node key %s carries no public key", path)
	}
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:chainNodeIDBytes]), nil
}

// validateChainStatus checks that /status came from this node's CometBFT and
// that what the report keeps from it has CometBFT's own shape. The errors
// name the field and never echo the value.
func validateChainStatus(st *chainStatus, wantNodeID string) error {
	if st.NodeInfo.ID != wantNodeID {
		return errors.New("chain RPC /status: node_info.id is not this node's id " +
			"(derived from its node key); something other than this node's chain answers on the RPC port")
	}
	if !chainIDRe.MatchString(st.NodeInfo.Network) {
		return errors.New("chain RPC /status: node_info.network is not a well-formed chain id")
	}
	if !chainNodeVersionRe.MatchString(st.NodeInfo.Version) {
		return errors.New("chain RPC /status: node_info.version is not a well-formed version")
	}
	return nil
}

// validateValidatorAddress checks one /validators entry's address.
func validateValidatorAddress(addr string) error {
	if !chainValidatorAddressRe.MatchString(addr) {
		return errors.New("chain RPC /validators: a validator address is not 40 upper-case hex characters")
	}
	return nil
}
