// Package deploysecrets stores a tenant deployment's environment file and
// workload token where systemd reads them for the deployment's unit.
//
// systemd reads EnvironmentFile= and LoadCredential= as PID 1, before it drops
// to the deployment's DynamicUser, and follows symlinks. They used to live in a
// directory the orama user owns, so a compromised gateway could replace one
// with a symlink to any root-readable file (/etc/shadow, the WireGuard private
// key), start the unit, and read the file through the deployment it controls.
// They now live in a directory only root can write, written by
// orama-privhelper with O_NOFOLLOW; the gateway hands over contents, never
// paths.
package deploysecrets

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// Dir is where the files live: root:root 0700, persistent across reboots so an
// enabled deployment unit finds its environment at boot.
const Dir = "/var/lib/orama-deploy"

// Kind is which file of a deployment.
type Kind string

const (
	Env   Kind = "env"
	Token Kind = "token"
)

// instancePattern is a deployment unit's instance name (%i), which names its
// files; it can never hold a path separator or start with a dot.
var instancePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,160}$`)

// ValidInstance reports whether instance can name a deployment's files.
func ValidInstance(instance string) bool { return instancePattern.MatchString(instance) }

// Path is the file of kind for instance, as the unit templates name it:
// orama-deploy-%i.env and orama-deploy-%i.token.
func Path(dir, instance string, kind Kind) string {
	return filepath.Join(dir, "orama-deploy-"+instance+"."+string(kind))
}

// Write stores data as instance's file of kind, 0600, replacing it atomically.
// The directory is created root-only and its mode converged; the file is
// written to a fresh random temp name (O_EXCL) in that root-only directory and
// renamed over the target.
func Write(dir, instance string, kind Kind, data []byte) error {
	if !ValidInstance(instance) {
		return fmt.Errorf("deployment instance %q is not a valid unit instance name", instance)
	}
	if kind != Env && kind != Token {
		return fmt.Errorf("unknown deployment file kind %q", kind)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("restrict %s: %w", dir, err)
	}
	// A random O_EXCL name in the root-only directory: two helper requests for
	// the same instance, which the socket serves in parallel, cannot collide
	// on it, and nobody else can create names there to plant a symlink.
	target := Path(dir, instance, kind)
	f, err := os.CreateTemp(dir, filepath.Base(target)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create a temporary file for %s: %w", target, err)
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("close %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, target); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("install %s: %w", target, err)
	}
	return nil
}

// Clear removes both of instance's files; missing files are not an error.
func Clear(dir, instance string) error {
	if !ValidInstance(instance) {
		return fmt.Errorf("deployment instance %q is not a valid unit instance name", instance)
	}
	for _, kind := range []Kind{Env, Token} {
		if err := os.Remove(Path(dir, instance, kind)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove %s file of %s: %w", kind, instance, err)
		}
	}
	return nil
}
