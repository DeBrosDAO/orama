// Package gatewaykeys stores the index gateway's signing keys where a tenant
// gateway cannot read them.
//
// Every gateway on a node runs as the orama user. A key file in the index
// gateway's state directory is mode 0600 and owned by that user, so a tenant
// gateway — the same uid — reads it. These files are root:root 0400. systemd
// loads them into the index unit only, through LoadCredential; no other unit
// has the credential, and the orama user cannot open the directory.
package gatewaykeys

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"

	"github.com/DeBrosOfficial/network/pkg/constants"
)

// Dir is the root-owned tree the keys live in: <Dir>/<namespace>/<file>.
const Dir = "/var/lib/orama-gateway-keys"

// MaxPEM is the most a signing key PEM may be. An RSA-2048 key is under 2KB.
const MaxPEM = 16 << 10

// dirMode is the tree's mode. The orama user is not root and not in a group
// that can traverse it.
const dirMode = 0o700

// fileMode is a key file's mode.
const fileMode = 0o400

// Names are the only files this tree holds: the index gateway's two signing keys.
func Names() []string {
	return []string{constants.GatewayRSAKeyFileName, constants.GatewayEdDSAKeyFileName}
}

// ValidName reports whether name is one of Names.
func ValidName(name string) bool {
	for _, n := range Names() {
		if name == n {
			return true
		}
	}
	return false
}

// Write stores pem at dir/namespace/name, replacing the file, as root:root 0400.
// namespace is the index gateway only; the caller has already checked that.
func Write(dir, namespace, name string, keyPEM []byte) error {
	if namespace != constants.IndexNamespace {
		return fmt.Errorf("gateway key namespace %q is not %s", namespace, constants.IndexNamespace)
	}
	if !ValidName(name) {
		return fmt.Errorf("gateway key %q is not a signing key", name)
	}
	if len(keyPEM) == 0 || len(keyPEM) > MaxPEM {
		return fmt.Errorf("gateway key %s is %d bytes", name, len(keyPEM))
	}
	destDir := filepath.Join(dir, namespace)
	if err := os.MkdirAll(destDir, dirMode); err != nil {
		return fmt.Errorf("create %s: %w", destDir, err)
	}
	if err := os.Chmod(destDir, dirMode); err != nil {
		return fmt.Errorf("restrict %s: %w", destDir, err)
	}
	tmp, err := os.CreateTemp(destDir, name+".tmp-*")
	if err != nil {
		return fmt.Errorf("create %s: %w", name, err)
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(fileMode); err != nil {
		tmp.Close()
		return fmt.Errorf("restrict %s: %w", name, err)
	}
	if _, err := tmp.Write(keyPEM); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", name, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", name, err)
	}
	dest := filepath.Join(destDir, name)
	if err := os.Rename(tmpName, dest); err != nil {
		return fmt.Errorf("replace %s: %w", dest, err)
	}
	cleanup = false
	if err := os.Chmod(dest, fileMode); err != nil {
		return fmt.Errorf("restrict %s: %w", dest, err)
	}
	return nil
}

// Ensure makes sure the index gateway's signing keys exist before its unit
// starts. The unit loads them with LoadCredential= and no prefix, so systemd
// refuses to start it while either file is missing: there is no "optional"
// form of the directive (a leading "-" is not valid syntax there, and systemd
// ignores the line, leaving the gateway with no credentials directory at all).
//
// A key that is already in dir is never touched: replacing it would invalidate
// every token the gateway signed with it. A missing key is taken from
// readLegacy, the copy an earlier release left in the gateway's state
// directory, so an upgrade carries the key it was running with; only when
// there is none is a new one generated. readLegacy returns (nil, nil) for "no
// such file". It returns the names it created.
func Ensure(dir string, readLegacy func(name string) ([]byte, error)) ([]string, error) {
	var created []string
	for _, name := range Names() {
		path := filepath.Join(dir, constants.IndexNamespace, name)
		info, err := os.Lstat(path)
		switch {
		case err == nil && info.Mode().IsRegular() && info.Size() > 0:
			continue
		case err == nil:
			return created, fmt.Errorf("the index gateway's signing key %s is not a non-empty regular file; "+
				"move it aside to have a new one generated, which invalidates every token this gateway has issued", path)
		case !os.IsNotExist(err):
			return created, fmt.Errorf("inspect the index gateway's signing key %s: %w", path, err)
		}
		keyPEM, err := readLegacy(name)
		if err != nil {
			return created, fmt.Errorf("read the index gateway's %s from its state directory: %w", name, err)
		}
		if len(keyPEM) == 0 {
			if keyPEM, err = Generate(name); err != nil {
				return created, err
			}
		}
		if err := Write(dir, constants.IndexNamespace, name, keyPEM); err != nil {
			return created, err
		}
		created = append(created, name)
	}
	return created, nil
}

// Generate makes a new signing key named name: an RSA-2048 key in PKCS#1 PEM
// for the RSA name, an Ed25519 key in PKCS#8 PEM for the EdDSA one.
func Generate(name string) ([]byte, error) {
	switch name {
	case constants.GatewayRSAKeyFileName:
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			return nil, fmt.Errorf("generate RSA key: %w", err)
		}
		return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), nil
	case constants.GatewayEdDSAKeyFileName:
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, fmt.Errorf("generate Ed25519 key: %w", err)
		}
		der, err := x509.MarshalPKCS8PrivateKey(priv)
		if err != nil {
			return nil, fmt.Errorf("marshal Ed25519 key: %w", err)
		}
		return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
	}
	return nil, fmt.Errorf("gateway key %q is not a signing key", name)
}
