package secrets

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// RegistryFileName is the run's token registry, beside the fleet state in the
// run's work dir (never in the artifact dir, which is shared): one credential
// per line, mode 0600. Feature processes append the credentials they mint;
// the runner reads it to redact what those processes printed and what the
// collector gathers after them.
const RegistryFileName = "redact-tokens"

// registryMode keeps the registry readable by the run's user only.
const registryMode = 0o600

// registryMu serialises appends within one process; O_APPEND of one short
// line keeps appends from different processes whole.
var registryMu sync.Mutex

// RegistryPath is the registry of the run whose state file is statePath.
func RegistryPath(statePath string) string {
	return filepath.Join(filepath.Dir(statePath), RegistryFileName)
}

// AppendRegistry appends values to the registry at path, one per line.
func AppendRegistry(path string, values []string) error {
	var b strings.Builder
	for _, v := range values {
		if strings.ContainsAny(v, "\r\n") {
			return fmt.Errorf("refusing to register a credential that spans lines in %s", path)
		}
		b.WriteString(v)
		b.WriteByte('\n')
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, registryMode)
	if err != nil {
		return fmt.Errorf("failed to open the token registry %s: %w", path, err)
	}
	if _, err := f.WriteString(b.String()); err != nil {
		return errors.Join(fmt.Errorf("failed to append to the token registry %s: %w", path, err), f.Close())
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("failed to close the token registry %s: %w", path, err)
	}
	return nil
}

// Registry read bounds: a credential line longer than maxRegistryLine is
// registered cut to it (its prefix still masks), and a registry over
// maxRegistryBytes is refused (the callers then withhold what they would
// have redacted with it).
const (
	maxRegistryLine  = 64 << 10
	maxRegistryBytes = 64 << 20
)

// LoadRegistry reads the registry at path; a missing file holds nothing.
func LoadRegistry(path string) ([]string, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to open the token registry %s: %w", path, err)
	}
	defer f.Close()
	if info, err := f.Stat(); err != nil || info.Size() > maxRegistryBytes {
		return nil, fmt.Errorf("the token registry %s cannot be read whole (over %d bytes, or stat failed: %v)", path, maxRegistryBytes, err)
	}
	var out []string
	r := bufio.NewReader(f)
	for {
		line, err := readBoundedLine(r, maxRegistryLine)
		if v := strings.TrimSpace(line); v != "" {
			out = append(out, v)
		}
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, fmt.Errorf("failed to read the token registry %s: %w", path, err)
		}
	}
}

// readBoundedLine reads one line, keeping at most limit bytes of it.
func readBoundedLine(r *bufio.Reader, limit int) (string, error) {
	var b strings.Builder
	for {
		chunk, isPrefix, err := r.ReadLine()
		if err != nil {
			return b.String(), err
		}
		if room := limit - b.Len(); room > 0 {
			b.Write(chunk[:min(len(chunk), room)])
		}
		if !isPrefix {
			return b.String(), nil
		}
	}
}

// ForRun is the redactor of the run whose state file is statePath: the secret
// environment (through lookup) plus every credential the registry holds.
func ForRun(lookup func(string) (string, bool), statePath string) (*Redactor, error) {
	r := FromEnv(lookup)
	vals, err := LoadRegistry(RegistryPath(statePath))
	if err != nil {
		return nil, err
	}
	if err := r.Add(vals...); err != nil {
		return nil, err
	}
	return r, nil
}

// PersistTo makes Add append every new value to the registry at path.
func (r *Redactor) PersistTo(path string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sink = func(values []string) error { return AppendRegistry(path, values) }
}
