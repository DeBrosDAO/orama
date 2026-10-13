//go:build e2e_fleet

// Package realistic holds what the G8 packages (reference-apps, soak, chaos,
// perf, scanners, open-network-phases, oramaos) share: commands run on the
// runner with an allowlisted environment and recorded as evidence, result
// files in the run's artifact dir, latency statistics and SLO checks, a
// mixed-traffic driver, and the chaos events the destructive packages fire.
package realistic

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// Artifact file modes: result files may name node addresses, so they are the
// runner user's alone, like the bootstrap baseline (features/internal/infra).
const (
	artifactDirMode  = 0o700
	artifactFileMode = 0o600
)

// ArtifactPath returns <artifact dir>/<feature>/<name>, creating the
// feature's directory. The report collects everything under the artifact dir.
func ArtifactPath(t testing.TB, f *fleet.Fleet, feature, name string) string {
	t.Helper()
	dir := filepath.Join(f.State.ArtifactDir, feature)
	if err := os.MkdirAll(dir, artifactDirMode); err != nil {
		t.Fatalf("failed to create the artifact directory %s: %v", dir, err)
	}
	return filepath.Join(dir, name)
}

// WriteJSON records v as <artifact dir>/<feature>/<name>, redacted: an
// evidence file must never carry a credential the run minted.
func WriteJSON(t testing.TB, f *fleet.Fleet, feature, name string, v any) string {
	t.Helper()
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("failed to encode %s: %v", name, err)
	}
	return WriteText(t, f, feature, name, string(raw))
}

// WriteText records s, redacted, as <artifact dir>/<feature>/<name>.
func WriteText(t testing.TB, f *fleet.Fleet, feature, name, s string) string {
	t.Helper()
	path := ArtifactPath(t, f, feature, name)
	if err := os.WriteFile(path, []byte(f.Redact(s)), artifactFileMode); err != nil {
		t.Fatalf("failed to write %s: %v", path, err)
	}
	return path
}

// ReadJSON decodes the JSON file at path into v.
func ReadJSON(path string, v any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", path, err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("failed to decode %s: %w", path, err)
	}
	return nil
}
