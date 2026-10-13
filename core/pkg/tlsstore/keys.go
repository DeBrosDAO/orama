// Package tlsstore is the cluster's shared certificate store: the storage
// every node's Caddy uses, so a certificate is obtained once per cluster and
// every node serves it (bugboard #751).
//
// Caddy reaches it through its `caddy.storage.orama` module (orama/caddy),
// which calls the local index gateway's /v1/internal/tls-store. The gateway
// keeps the values in the cluster registry, where every node reads the same
// rows. A value is sealed by Caddy before it leaves the process and opened
// only by Caddy and by the exporter that hands the wildcard to TURN, so the
// rows, their snapshots and backups never hold a private key in the clear.
//
// The wire format, the key derivation and the sealing are written twice: here
// and in orama/caddy, which is its own Go module and cannot import this one.
// Both carry the same test vectors, so a change to one fails the other's
// tests.
package tlsstore

import (
	"crypto/sha256"
	"fmt"
	"io"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/secrets"
	"golang.org/x/crypto/hkdf"
)

const (
	// masterKeyPurpose separates the store's key from every other key derived
	// from the cluster secret. Install writes the result to MasterKeyPath for
	// Caddy; the gateway derives it again.
	masterKeyPurpose = "caddy-tls-store"

	// macKeyInfo and sealKeyInfo split the master key in two, so the key that
	// authenticates a call to the store is never the key that seals what it
	// holds.
	macKeyInfo  = "orama-tls-store-mac-v1"
	sealKeyInfo = "orama-tls-store-seal-v1"

	// MACAudience is the audience the store's calls are stamped for. A
	// coordination stamp names the node it is for; these calls never leave the
	// node, and the key they are made with authorises nothing else.
	MACAudience = "caddy-tls-store"

	// keyLen is the length of every derived key.
	keyLen = 32
)

// Keys are the two keys the store is used with.
type Keys struct {
	// MAC authenticates a call to /v1/internal/tls-store.
	MAC []byte
	// Seal encrypts and authenticates a stored value.
	Seal []byte
}

// MasterKey derives the store's master key from the cluster secret. The secret
// is trimmed for the reason auth.CoordinationKey gives.
func MasterKey(clusterSecret string) ([]byte, error) {
	key, err := secrets.DeriveKey(strings.TrimSpace(clusterSecret), masterKeyPurpose)
	if err != nil {
		return nil, fmt.Errorf("no TLS store key: this node has no cluster secret, so it cannot read the cluster's certificates: %w", err)
	}
	return key, nil
}

// DeriveKeys splits a master key into the MAC and sealing keys.
func DeriveKeys(master []byte) (Keys, error) {
	if len(master) != keyLen {
		return Keys{}, fmt.Errorf("TLS store master key is %d bytes, want %d", len(master), keyLen)
	}
	mac, err := expand(master, macKeyInfo)
	if err != nil {
		return Keys{}, err
	}
	seal, err := expand(master, sealKeyInfo)
	if err != nil {
		return Keys{}, err
	}
	return Keys{MAC: mac, Seal: seal}, nil
}

// KeysFromClusterSecret is MasterKey followed by DeriveKeys.
func KeysFromClusterSecret(clusterSecret string) (Keys, error) {
	master, err := MasterKey(clusterSecret)
	if err != nil {
		return Keys{}, err
	}
	return DeriveKeys(master)
}

func expand(master []byte, info string) ([]byte, error) {
	out := make([]byte, keyLen)
	if _, err := io.ReadFull(hkdf.Expand(sha256.New, master, []byte(info)), out); err != nil {
		return nil, fmt.Errorf("derive the TLS store %s key: %w", info, err)
	}
	return out, nil
}
