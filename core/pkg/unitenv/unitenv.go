// Package unitenv stores the environment files of Orama's namespace units
// (orama-namespace-<svc>@<namespace>) where systemd reads them.
//
// systemd reads EnvironmentFile= as PID 1 and follows symlinks. The files used
// to live under /opt/orama/.orama/data/namespaces/<ns>/, which the orama user
// owns, so a compromised orama-node or gateway could create a namespace
// directory, symlink its <svc>.env to a root-only KEY=VALUE file — the
// WireGuard private key in /etc/wireguard/wg0.conf — start the unit through
// orama-privhelper, and read the key from the process environment. The files
// now live in a root-owned tree: the orama group may read them (the node
// compares contents and mtimes) but only root, through orama-privhelper, may
// create or replace them.
package unitenv

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// Dir is the root of the tree: <Dir>/<namespace>/<service>.env.
const Dir = "/var/lib/orama-unit-env"

// Modes: root owns everything; the orama group reads.
const (
	dirMode  os.FileMode = 0o750
	fileMode os.FileMode = 0o640
)

var (
	namespacePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)
	servicePattern   = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
)

// Owner is who the files belong to: root and the orama group on a node.
type Owner struct {
	UID, GID int
}

// Valid reports whether namespace and service can name an env file.
func Valid(namespace, service string) bool {
	return namespacePattern.MatchString(namespace) && servicePattern.MatchString(service)
}

// Path is the env file of service in namespace.
func Path(dir, namespace, service string) string {
	return filepath.Join(dir, namespace, service+".env")
}

// Write stores data as the env file of service in namespace, replacing it
// atomically, with every directory and the file owned by owner.
func Write(dir, namespace, service string, data []byte, owner Owner) error {
	if !Valid(namespace, service) {
		return fmt.Errorf("unit env %q/%q is not a valid namespace/service name", namespace, service)
	}
	nsDir := filepath.Join(dir, namespace)
	for _, d := range []string{dir, nsDir} {
		if err := os.MkdirAll(d, dirMode); err != nil {
			return fmt.Errorf("create %s: %w", d, err)
		}
		if err := own(d, dirMode, owner); err != nil {
			return err
		}
	}
	tmp, err := os.CreateTemp(nsDir, "."+service+".env-*")
	if err != nil {
		return fmt.Errorf("create temp env in %s: %w", nsDir, err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmpPath, err)
	}
	if err := own(tmpPath, fileMode, owner); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, Path(dir, namespace, service)); err != nil {
		return fmt.Errorf("install env of %s/%s: %w", namespace, service, err)
	}
	return nil
}

// ClearNamespace removes every env file of namespace.
func ClearNamespace(dir, namespace string) error {
	if !namespacePattern.MatchString(namespace) {
		return fmt.Errorf("namespace %q is not valid", namespace)
	}
	if err := os.RemoveAll(filepath.Join(dir, namespace)); err != nil {
		return fmt.Errorf("remove env files of %s: %w", namespace, err)
	}
	return nil
}

func own(path string, mode os.FileMode, owner Owner) error {
	if err := os.Chown(path, owner.UID, owner.GID); err != nil {
		return fmt.Errorf("chown %s: %w", path, err)
	}
	if err := os.Chmod(path, mode); err != nil {
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	return nil
}
