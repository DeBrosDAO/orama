package ipfs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func secretPaths(t *testing.T) (secretPath, clusterPath string) {
	t.Helper()
	dir := t.TempDir()
	clusterPath = filepath.Join(dir, "ipfs-cluster")
	if err := os.MkdirAll(clusterPath, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	return filepath.Join(dir, "cluster-secret"), clusterPath
}

// A node that has never joined anything may mint a secret. That is the genesis
// case and the only one where generating is correct.
func TestGeneratesOnlyForAFreshNode(t *testing.T) {
	secretPath, clusterPath := secretPaths(t)

	got, err := loadOrGenerateClusterSecret(secretPath, clusterPath)
	if err != nil {
		t.Fatalf("fresh node: %v", err)
	}
	if len(got) != clusterSecretHexLen {
		t.Errorf("generated secret is %d chars, want %d", len(got), clusterSecretHexLen)
	}
	// It must be persisted, or the next start generates a different one.
	onDisk, err := os.ReadFile(secretPath)
	if err != nil {
		t.Fatalf("secret not written: %v", err)
	}
	if strings.TrimSpace(string(onDisk)) != got {
		t.Error("returned secret does not match what was written")
	}
	if info, err := os.Stat(secretPath); err == nil && info.Mode().Perm() != 0o600 {
		t.Errorf("secret mode = %o, want 600", info.Mode().Perm())
	}
}

func TestReturnsTheExistingSecretUnchanged(t *testing.T) {
	secretPath, clusterPath := secretPaths(t)
	want := strings.Repeat("a", clusterSecretHexLen)
	if err := os.WriteFile(secretPath, []byte(want+"\n"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	got, err := loadOrGenerateClusterSecret(secretPath, clusterPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got != want {
		t.Errorf("secret = %q, want the stored value (trailing newline trimmed)", got)
	}
}

// The dangerous case: a node that has joined before, whose secret has gone
// missing. Generating one here silently cuts it out of the private network,
// which reports as a generic connection failure and nothing else.
func TestRefusesToGenerateForANodeThatHasJoined(t *testing.T) {
	secretPath, clusterPath := secretPaths(t)
	if err := os.WriteFile(filepath.Join(clusterPath, "identity.json"), []byte(`{"id":"x"}`), 0o600); err != nil {
		t.Fatalf("seed identity: %v", err)
	}

	_, err := loadOrGenerateClusterSecret(secretPath, clusterPath)
	if err == nil {
		t.Fatal("a node with an existing cluster identity was given a brand-new secret")
	}
	if !strings.Contains(err.Error(), "identity") {
		t.Errorf("error = %v, want it to name the identity as the reason", err)
	}
	if _, statErr := os.Stat(secretPath); statErr == nil {
		t.Error("a secret was written despite the refusal")
	}
}

// An unreadable file may well be correct. Replacing it is the one response
// guaranteed to be wrong.
func TestUnreadableSecretIsNotReplaced(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits are not enforced")
	}
	secretPath, clusterPath := secretPaths(t)
	original := strings.Repeat("b", clusterSecretHexLen)
	if err := os.WriteFile(secretPath, []byte(original), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := os.Chmod(secretPath, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	defer os.Chmod(secretPath, 0o600)

	if _, err := loadOrGenerateClusterSecret(secretPath, clusterPath); err == nil {
		t.Fatal("an unreadable secret was silently replaced")
	}

	_ = os.Chmod(secretPath, 0o600)
	after, err := os.ReadFile(secretPath)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if strings.TrimSpace(string(after)) != original {
		t.Error("the unreadable secret was overwritten")
	}
}

// A wrong-length value is more likely a corrupted or re-encoded copy of the
// real secret than a reason to invent a new network.
func TestWrongLengthSecretIsRefusedNotReplaced(t *testing.T) {
	secretPath, clusterPath := secretPaths(t)
	truncated := strings.Repeat("c", clusterSecretHexLen-1)
	if err := os.WriteFile(secretPath, []byte(truncated), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	_, err := loadOrGenerateClusterSecret(secretPath, clusterPath)
	if err == nil {
		t.Fatal("a wrong-length secret was silently replaced")
	}
	if !strings.Contains(err.Error(), "63") {
		t.Errorf("error = %v, want it to name the observed length", err)
	}
	after, _ := os.ReadFile(secretPath)
	if string(after) != truncated {
		t.Error("the malformed secret was overwritten")
	}
}

// A node with a non-empty service.json has been configured into a cluster even
// if identity.json is absent.
func TestServiceJSONAlsoCountsAsHavingJoined(t *testing.T) {
	secretPath, clusterPath := secretPaths(t)
	if err := os.WriteFile(filepath.Join(clusterPath, "service.json"), []byte(`{"cluster":{}}`), 0o600); err != nil {
		t.Fatalf("seed service.json: %v", err)
	}
	if _, err := loadOrGenerateClusterSecret(secretPath, clusterPath); err == nil {
		t.Fatal("a node with a service.json was given a brand-new secret")
	}
}

// An empty service.json is what a half-finished install leaves behind; it is
// not evidence of membership.
func TestEmptyClusterFilesDoNotBlockGenesis(t *testing.T) {
	secretPath, clusterPath := secretPaths(t)
	if err := os.WriteFile(filepath.Join(clusterPath, "service.json"), nil, 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := loadOrGenerateClusterSecret(secretPath, clusterPath); err != nil {
		t.Fatalf("empty files should not count as membership: %v", err)
	}
}
