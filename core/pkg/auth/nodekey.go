package auth

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
)

// A node's own key lives on the node and nowhere else. This is how it is kept.
//
// It is generated on first use rather than handed out at join, so the private
// half never crosses the network and the cluster never holds a copy — the join
// bundle already hands a machine every shared secret the cluster has, and
// adding a per-node private key to it would recreate the problem the key
// exists to solve.

// nodeKeyFileMode is 0600. The file is the node's identity: anything that can
// read it can speak as this node until an operator revokes it.
const nodeKeyFileMode = 0o600

// NodeKeyPath is where a node keeps its key, given the orama directory.
func NodeKeyPath(oramaDir string) string {
	return filepath.Join(oramaDir, "secrets", "node-key.pem")
}

// LoadOrCreateNodeKey returns this node's key, generating one the first time.
//
// A file that exists and cannot be read is an error, never a reason to generate
// a second key: a node that quietly re-keyed would be refused by the cluster
// (its recorded key would no longer match) and the reason would be invisible.
func LoadOrCreateNodeKey(oramaDir string) (*NodeKeyPair, error) {
	path := NodeKeyPath(oramaDir)

	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		// O_EXCL and 0600 govern the file this process creates. They say
		// nothing about one restored from a backup, unpacked by a deploy, or
		// left behind by an earlier release — and this file is the node's
		// identity, so a readable copy is a machine that can be impersonated.
		if err := refuseIfReadable(path); err != nil {
			return nil, err
		}
		key, parseErr := parseNodeKey(raw)
		if parseErr != nil {
			return nil, fmt.Errorf("this node's key at %s could not be read; it identifies this "+
				"node to the cluster, so it must be repaired or the node re-admitted: %w", path, parseErr)
		}
		return key, nil
	case !os.IsNotExist(err):
		return nil, fmt.Errorf("read this node's key at %s: %w", path, err)
	}

	key, err := NewNodeKeyPair()
	if err != nil {
		return nil, err
	}
	if err := writeNodeKey(path, key); err != nil {
		return nil, err
	}
	return key, nil
}

// parseNodeKey reads the PEM form.
func parseNodeKey(raw []byte) (*NodeKeyPair, error) {
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, fmt.Errorf("not a PEM file")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("not a PKCS#8 private key: %w", err)
	}
	priv, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("not an Ed25519 private key")
	}
	return NodeKeyPairFrom(priv)
}

// refuseIfReadable refuses a key file anyone but its owner can read.
func refuseIfReadable(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("check the permissions on this node's key at %s: %w", path, err)
	}
	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		return fmt.Errorf("this node's key at %s is mode %#o; it identifies this node to the "+
			"cluster and must be %#o", path, mode, nodeKeyFileMode)
	}
	return nil
}

// writeNodeKey stores a freshly generated key.
//
// Written to a temporary file and renamed, because a half-written key is worse
// than no key: the next start reads it, fails to parse it, and refuses to
// re-key — correctly, since a silent re-key would be refused by the cluster —
// so a crash between creating the file and filling it would leave the node
// permanently unable to register until a human deleted it. A rename is atomic,
// so the file at `path` is either absent or complete.
//
// The temporary file is created O_EXCL under this process's pid, so two starts
// racing each other cannot write through one another; the rename then settles
// which key the node keeps, and the loser reads the winner's on its next pass.
func writeNodeKey(path string, key *NodeKeyPair) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create the secrets directory for this node's key: %w", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key.PrivateKey())
	if err != nil {
		return fmt.Errorf("encode this node's key: %w", err)
	}
	encoded := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})

	tmp := fmt.Sprintf("%s.%d.tmp", path, os.Getpid())
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, nodeKeyFileMode)
	if err != nil {
		return fmt.Errorf("write this node's key to %s: %w", tmp, err)
	}
	// Removing the temporary file is unconditional: on the success path the
	// rename has already moved it, and Remove on a name that is gone is not an
	// error worth reporting over whatever brought us here.
	defer func() { _ = os.Remove(tmp) }()

	if _, err := f.Write(encoded); err != nil {
		f.Close()
		return fmt.Errorf("write this node's key to %s: %w", tmp, err)
	}
	// Sync before rename: the rename can otherwise be durable while the bytes
	// are not, which is the same zero-byte file by another route.
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("flush this node's key to %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close this node's key at %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("put this node's key in place at %s: %w", path, err)
	}
	return syncDir(dir)
}

// syncDir makes the rename itself durable.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open %s to make this node's key durable: %w", dir, err)
	}
	defer d.Close()
	if err := d.Sync(); err != nil {
		return fmt.Errorf("flush %s so this node's key survives a restart: %w", dir, err)
	}
	return nil
}
