package install

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/rootfs"
)

// Install and upgrade run as root over the orama user's .orama tree. These
// cover the writers and readers a compromised orama process would aim a
// symlink at: each must refuse it and leave the target alone.

const symlinkTargetContent = "root-only"

// plantedTree is an .orama tree with configs/ and secrets/, and a root-only
// file outside it for symlinks to point at.
func plantedTree(t *testing.T) (oramaDir, target string) {
	t.Helper()
	oramaDir = filepath.Join(t.TempDir(), ".orama")
	for _, d := range []string{"configs", "secrets", "data"} {
		if err := os.MkdirAll(filepath.Join(oramaDir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	target = filepath.Join(t.TempDir(), "sudoers")
	if err := os.WriteFile(target, []byte(symlinkTargetContent), 0o440); err != nil {
		t.Fatal(err)
	}
	return oramaDir, target
}

func plantSymlink(t *testing.T, target, path string) {
	t.Helper()
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
}

func requireRefused(t *testing.T, err error, target string) {
	t.Helper()
	if !errors.Is(err, rootfs.ErrSymlink) {
		t.Fatalf("err = %v, want a refused symlink", err)
	}
	data, readErr := os.ReadFile(target)
	if readErr != nil || string(data) != symlinkTargetContent {
		t.Fatalf("symlink target changed: %q, %v", data, readErr)
	}
	if info, statErr := os.Stat(target); statErr != nil || info.Mode().Perm() != 0o440 {
		t.Fatalf("symlink target mode changed: %v, %v", info, statErr)
	}
}

func TestSaveConfig_symlinkedNodeYAMLRefused(t *testing.T) {
	oramaDir, target := plantedTree(t)
	plantSymlink(t, target, filepath.Join(oramaDir, "configs", "node.yaml"))
	err := NewSecretGenerator(oramaDir).SaveConfig("node.yaml", "node: {}\n")
	requireRefused(t, err, target)
}

func TestSaveConfig_symlinkedConfigsDirRefused(t *testing.T) {
	oramaDir, target := plantedTree(t)
	configs := filepath.Join(oramaDir, "configs")
	if err := os.Remove(configs); err != nil {
		t.Fatal(err)
	}
	plantSymlink(t, filepath.Dir(target), configs)
	err := NewSecretGenerator(oramaDir).SaveConfig(filepath.Base(target), "pwned\n")
	requireRefused(t, err, target)
}

func TestSaveConfig_rewritesAt0600(t *testing.T) {
	oramaDir, _ := plantedTree(t)
	path := filepath.Join(oramaDir, "configs", "node.yaml")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := NewSecretGenerator(oramaDir).SaveConfig("node.yaml", "new"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("node.yaml mode = %v, want 0600", info.Mode().Perm())
	}
}

// A secret the orama user replaced with a symlink is an error, never a secret
// read from, or regenerated into, the target.
func TestEnsureSecrets_symlinkedSecretRefused(t *testing.T) {
	cases := map[string]func(*SecretGenerator) error{
		"cluster-secret":         func(sg *SecretGenerator) error { _, err := sg.EnsureClusterSecret(); return err },
		"swarm.key":              func(sg *SecretGenerator) error { _, err := sg.EnsureSwarmKey(); return err },
		"rqlite-password":        func(sg *SecretGenerator) error { _, _, err := sg.EnsureRQLiteAuth(); return err },
		"api-key-hmac-secret":    func(sg *SecretGenerator) error { _, err := sg.EnsureAPIKeyHMACSecret(); return err },
		"secrets-encryption-key": func(sg *SecretGenerator) error { _, err := sg.EnsureSecretsEncryptionKey(); return err },
		"turn-secret":            func(sg *SecretGenerator) error { _, err := sg.EnsureTURNSecret(); return err },
	}
	for name, ensure := range cases {
		t.Run(name, func(t *testing.T) {
			oramaDir, target := plantedTree(t)
			plantSymlink(t, target, filepath.Join(oramaDir, "secrets", name))
			requireRefused(t, ensure(NewSecretGenerator(oramaDir)), target)
		})
	}
}

func TestEnsureNodeIdentity_symlinkedKeyRefused(t *testing.T) {
	oramaDir, target := plantedTree(t)
	plantSymlink(t, target, filepath.Join(oramaDir, "data", "identity.key"))
	_, err := NewSecretGenerator(oramaDir).EnsureNodeIdentity()
	requireRefused(t, err, target)
}

// Reading node.yaml through a symlink would put a root-only file's contents in
// an error message or a generated config.
func TestACMECA_symlinkedNodeYAMLRefused(t *testing.T) {
	oramaDir, target := plantedTree(t)
	plantSymlink(t, target, filepath.Join(oramaDir, "configs", "node.yaml"))
	_, err := NewConfigGenerator(oramaDir).ACMECA()
	requireRefused(t, err, target)
}

func TestEnsureDirectoryStructure_symlinkedSecretsDirRefused(t *testing.T) {
	oramaDir, target := plantedTree(t)
	secrets := filepath.Join(oramaDir, "secrets")
	if err := os.Remove(secrets); err != nil {
		t.Fatal(err)
	}
	plantSymlink(t, filepath.Dir(target), secrets)
	err := NewFilesystemProvisioner(filepath.Dir(oramaDir)).EnsureDirectoryStructure()
	requireRefused(t, err, target)
	if info, _ := os.Stat(filepath.Dir(target)); info.Mode().Perm() == 0o700 {
		t.Error("the symlink target directory was chmodded")
	}
}

func TestSavePreferences_symlinkRefused(t *testing.T) {
	oramaDir, target := plantedTree(t)
	plantSymlink(t, target, filepath.Join(oramaDir, preferencesFile))
	requireRefused(t, SavePreferences(oramaDir, &NodePreferences{Branch: "main"}), target)
}

func TestSavePreferences_createsMissingTree(t *testing.T) {
	oramaDir := filepath.Join(t.TempDir(), "opt-orama", ".orama")
	if err := SavePreferences(oramaDir, &NodePreferences{Branch: "dev", Nameserver: true}); err != nil {
		t.Fatal(err)
	}
	if got := LoadPreferences(oramaDir); got.Branch != "dev" || !got.Nameserver {
		t.Errorf("preferences = %+v", got)
	}
}
