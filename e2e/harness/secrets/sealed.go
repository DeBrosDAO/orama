package secrets

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
)

// SealedPrefixes are variable name prefixes sealed like SecretEnvNames: the
// secret store's own credentials.
var SealedPrefixes = []string{"INFISICAL_"}

// maxSealedBytes bounds the sealed secrets read from a pipe.
const maxSealedBytes = 1 << 20

// sealed holds the secret environment of a process that removed it from its
// own environ (see Seal): the values live in memory only, so os.Environ(),
// every child that inherits it, and /proc/<pid>/environ of a re-executed
// process carry none of them.
var sealed struct {
	mu     sync.RWMutex
	values map[string]string
}

// IsSecretEnv reports whether name is a secret variable: one of
// SecretEnvNames or a SealedPrefixes name.
func IsSecretEnv(name string) bool {
	if slices.Contains(SecretEnvNames, name) {
		return true
	}
	for _, p := range SealedPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// SplitSecretEnv separates environ (KEY=VALUE pairs) into the pairs that are
// not secret and the secret values by name.
func SplitSecretEnv(environ []string) ([]string, map[string]string) {
	var clean []string
	secret := map[string]string{}
	for _, kv := range environ {
		name, v, ok := strings.Cut(kv, "=")
		if ok && IsSecretEnv(name) {
			secret[name] = v
			continue
		}
		clean = append(clean, kv)
	}
	return clean, secret
}

// Seal makes LookupEnv answer values for their names (a copy is kept) and
// unsets those names in this process's environment, so no child started
// from os.Environ() inherits them.
func Seal(values map[string]string) error {
	sealed.mu.Lock()
	defer sealed.mu.Unlock()
	if sealed.values == nil {
		sealed.values = map[string]string{}
	}
	for name, v := range values {
		sealed.values[name] = v
		if err := os.Unsetenv(name); err != nil {
			return fmt.Errorf("failed to remove %s from the environment: %w", name, err)
		}
	}
	return nil
}

// Unseal forgets the sealed values of names (a process, or a test, that no
// longer needs them).
func Unseal(names ...string) {
	sealed.mu.Lock()
	defer sealed.mu.Unlock()
	for _, name := range names {
		delete(sealed.values, name)
	}
}

// Sealed returns a copy of the sealed values.
func Sealed() map[string]string {
	sealed.mu.RLock()
	defer sealed.mu.RUnlock()
	out := make(map[string]string, len(sealed.values))
	for k, v := range sealed.values {
		out[k] = v
	}
	return out
}

// LookupEnv is os.LookupEnv that also sees sealed values (which win). Code
// that reads a secret variable uses it instead of os.LookupEnv.
func LookupEnv(name string) (string, bool) {
	sealed.mu.RLock()
	v, ok := sealed.values[name]
	sealed.mu.RUnlock()
	if ok {
		return v, true
	}
	return os.LookupEnv(name)
}

// Getenv is LookupEnv's value, empty when unset.
func Getenv(name string) string {
	v, _ := LookupEnv(name)
	return v
}

// WriteSealed writes values to w (a pipe to the process that reads them
// with ReadSealed) as one JSON object.
func WriteSealed(w io.Writer, values map[string]string) error {
	if err := json.NewEncoder(w).Encode(values); err != nil {
		return fmt.Errorf("failed to pass the sealed secrets: %w", err)
	}
	return nil
}

// ReadSealed reads the values WriteSealed wrote, bounded.
func ReadSealed(r io.Reader) (map[string]string, error) {
	raw, err := io.ReadAll(io.LimitReader(r, maxSealedBytes+1))
	if err != nil {
		return nil, fmt.Errorf("failed to read the sealed secrets: %w", err)
	}
	if len(raw) > maxSealedBytes {
		return nil, fmt.Errorf("the sealed secrets are over %d bytes", maxSealedBytes)
	}
	values := map[string]string{}
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, fmt.Errorf("failed to decode the sealed secrets: %w", err)
	}
	for name := range values {
		if !IsSecretEnv(name) {
			return nil, fmt.Errorf("the sealed secrets carry %q, which is not a secret variable", name)
		}
	}
	return values, nil
}
