package install

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/cosmovisor"
)

func TestRenderGlobalChainUnit_runsOramadWithTheVerifierOfTheVersionCosmovisorRuns(t *testing.T) {
	unit := RenderGlobalChainUnit("")
	want := "--shielded-verifier " + constants.ChainHome + "/cosmovisor/current/bin/orama-orchard-verifier"
	if !strings.Contains(unit, want) {
		t.Fatalf("the chain unit lacks %q:\n%s", want, unit)
	}
	// Through current/, never genesis/ or upgrades/<name>: an upgrade changes
	// the link, not the unit.
	if strings.Contains(unit, "genesis/bin/orama-orchard-verifier") || strings.Contains(unit, "upgrades/") {
		t.Fatalf("the verifier path names a fixed version:\n%s", unit)
	}
}

func TestInstallGlobal_theVerifierIsStagedBesideOramadAndNotInstalledInTheBinDirectory(t *testing.T) {
	f := newGlobalFixture(t)
	if err := InstallGlobal(f.options(GlobalServiceChain), f.host); err != nil {
		t.Fatal(err)
	}
	if len(f.stages) != 1 || len(f.stages[0].companions) != 1 || f.stages[0].companions[0].Sum != f.verifierSum {
		t.Fatalf("stage calls = %+v", f.stages)
	}
	if _, err := os.Stat(filepath.Join(f.host.BinDir, "orama-orchard-verifier")); err == nil {
		t.Fatal("the verifier was installed next to the global binaries, where oramad does not look")
	}
}

// Every refusal below comes before the host changes: no account, no binary,
// no unit.
func TestInstallGlobal_aStagedDirectoryThatIsNotTheReleaseIsRefused(t *testing.T) {
	cases := map[string]struct {
		change func(t *testing.T, f *globalFixture)
		want   string
	}{
		"an oramad changed after the release was extracted": {
			func(t *testing.T, f *globalFixture) {
				writeStaged(t, f, "oramad", "binary oramad pinning "+f.verifierSum+" backdoor")
			},
			"is not the file the release shipped",
		},
		"a verifier changed after the release was extracted": {
			func(t *testing.T, f *globalFixture) { writeStaged(t, f, "orama-orchard-verifier", "another verifier") },
			"is not the file the release shipped",
		},
		"a missing verifier": {
			func(t *testing.T, f *globalFixture) { removeStaged(t, f, "orama-orchard-verifier") },
			"orama-orchard-verifier",
		},
		"a missing digest file": {
			func(t *testing.T, f *globalFixture) { removeStaged(t, f, "orama-orchard-verifier.sha256") },
			"orama-orchard-verifier.sha256",
		},
		"a digest file for another verifier": {
			func(t *testing.T, f *globalFixture) {
				f.restage(t, "orama-orchard-verifier.sha256", strings.Repeat("ab", 32)+"\n")
			},
			"says " + strings.Repeat("ab", 32),
		},
		"an oramad that pins another verifier": {
			func(t *testing.T, f *globalFixture) {
				f.restage(t, "oramad", "binary oramad pinning "+strings.Repeat("cd", 32))
			},
			"does not pin",
		},
		"an oramad the manifest does not list": {
			func(t *testing.T, f *globalFixture) { dropFromManifest(t, f, "oramad") },
			"does not list oramad",
		},
		"a verifier the manifest does not list": {
			func(t *testing.T, f *globalFixture) { dropFromManifest(t, f, "orama-orchard-verifier") },
			"does not list orama-orchard-verifier",
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := newGlobalFixture(t)
			c.change(t, f)
			err := InstallGlobal(f.options(GlobalServiceChain), f.host)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want one containing %q", err, c.want)
			}
			assertHostUntouched(t, f)
		})
	}
}

func TestInstallGlobal_everyBinaryItInstallsIsHeldToTheManifest(t *testing.T) {
	for _, name := range []string{"orama", "orama-global", "ipfs"} {
		f := newGlobalFixture(t)
		services := []GlobalService{GlobalServiceChain, GlobalServiceIPFS, GlobalServiceProvider}
		// Tampered after the manifest was written: written without re-listing.
		writeStaged(t, f, name, "tampered "+name)
		err := InstallGlobal(f.options(services...), f.host)
		if err == nil || !strings.Contains(err.Error(), "is not the file the release shipped") {
			t.Errorf("%s: err = %v", name, err)
		}
		if _, statErr := os.Stat(filepath.Join(f.host.BinDir, name)); statErr == nil {
			t.Errorf("a tampered %s was installed", name)
		}
	}
}

func TestInstallGlobal_theManifestIsRequiredAndMustBeReadable(t *testing.T) {
	f := newGlobalFixture(t)
	opts := f.options(GlobalServiceChain)
	opts.Manifest = ""
	if err := InstallGlobal(opts, f.host); err == nil || !strings.Contains(err.Error(), "manifest is required") {
		t.Fatalf("no manifest: %v", err)
	}
	opts.Manifest = "relative/manifest.json"
	if err := InstallGlobal(opts, f.host); err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("relative manifest: %v", err)
	}
	opts.Manifest = filepath.Join(t.TempDir(), "manifest.json")
	if err := InstallGlobal(opts, f.host); err == nil || !strings.Contains(err.Error(), "read the release manifest") {
		t.Fatalf("missing manifest: %v", err)
	}
	if err := os.WriteFile(opts.Manifest, []byte(`{"version":"1","checksums":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := InstallGlobal(opts, f.host); err == nil || !strings.Contains(err.Error(), "lists no bin/ files") {
		t.Fatalf("empty manifest: %v", err)
	}
	if err := os.WriteFile(opts.Manifest, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := InstallGlobal(opts, f.host); err == nil {
		t.Fatal("a manifest that is not JSON was accepted")
	}
	assertHostUntouched(t, f)
}

func TestStageMissingCompanions_aVerifierAlreadyStagedIsLeftAndADifferentOneRefused(t *testing.T) {
	home := t.TempDir()
	layout := cosmovisor.Layout{Home: home, Daemon: constants.ChainDaemonName}
	if err := os.MkdirAll(layout.GenesisBinDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layout.GenesisBinDir(), "orama-orchard-verifier"), []byte("v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("v1"))
	same := []StagedFile{{Name: "orama-orchard-verifier", Sum: hex.EncodeToString(sum[:])}}
	if err := stageMissingCompanions(layout, same); err != nil {
		t.Fatalf("a verifier that is already there: %v", err)
	}
	other := []StagedFile{{Name: "orama-orchard-verifier", Sum: strings.Repeat("00", 32)}}
	if err := stageMissingCompanions(layout, other); err == nil || !strings.Contains(err.Error(), "already staged with different bytes") {
		t.Fatalf("a different verifier over an installed one: %v", err)
	}
}

func writeStaged(t *testing.T, f *globalFixture, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.staged, name), []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
}

func removeStaged(t *testing.T, f *globalFixture, name string) {
	t.Helper()
	if err := os.Remove(filepath.Join(f.staged, name)); err != nil {
		t.Fatal(err)
	}
	f.writeManifest(t)
}

// dropFromManifest rewrites the manifest without name.
func dropFromManifest(t *testing.T, f *globalFixture, name string) {
	t.Helper()
	hidden := filepath.Join(t.TempDir(), name)
	if err := os.Rename(filepath.Join(f.staged, name), hidden); err != nil {
		t.Fatal(err)
	}
	f.writeManifest(t)
	if err := os.Rename(hidden, filepath.Join(f.staged, name)); err != nil {
		t.Fatal(err)
	}
}

// assertHostUntouched fails if the install created an account, a binary, a
// unit or staged anything.
func assertHostUntouched(t *testing.T, f *globalFixture) {
	t.Helper()
	if len(f.stages) != 0 {
		t.Errorf("oramad was staged: %v", f.stages)
	}
	if entries, _ := os.ReadDir(f.host.BinDir); len(entries) != 0 {
		t.Errorf("binaries were installed: %v", entries)
	}
	if entries, _ := os.ReadDir(f.host.UnitDir); len(entries) != 0 {
		t.Errorf("units were written: %v", entries)
	}
	if calls := f.node.named("useradd"); len(calls) != 0 {
		t.Errorf("accounts were created: %v", calls)
	}
}

func TestInstallGlobal_aNodeRunningAnUpgradeStagedWithoutAVerifierIsRefusedUntilItIsRestaged(t *testing.T) {
	f := newGlobalFixture(t)
	root := filepath.Join(f.host.ChainHome, "cosmovisor")
	bin := filepath.Join(root, "upgrades", "v2", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("upgrades/v2", filepath.Join(root, "current")); err != nil {
		t.Fatal(err)
	}
	err := InstallGlobal(f.options(GlobalServiceChain), f.host)
	if err == nil || !strings.Contains(err.Error(), "stage that upgrade again") {
		t.Fatalf("err = %v", err)
	}
	assertHostUntouched(t, f)

	if err := os.WriteFile(filepath.Join(bin, "orama-orchard-verifier"), []byte("v"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := InstallGlobal(f.options(GlobalServiceChain), f.host); err != nil {
		t.Fatalf("an upgrade that has its verifier: %v", err)
	}
}

func TestCheckCurrentHasVerifier_genesisAbsentAndEscapingLinks(t *testing.T) {
	layout := cosmovisor.Layout{Home: t.TempDir(), Daemon: constants.ChainDaemonName}
	if err := checkCurrentHasVerifier(layout); err != nil {
		t.Fatalf("no current yet: %v", err)
	}
	if err := os.MkdirAll(layout.Root(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("genesis", layout.Current()); err != nil {
		t.Fatal(err)
	}
	if err := checkCurrentHasVerifier(layout); err != nil {
		t.Fatalf("current at genesis: %v", err)
	}
	if err := os.Remove(layout.Current()); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../etc", layout.Current()); err != nil {
		t.Fatal(err)
	}
	if err := checkCurrentHasVerifier(layout); err == nil {
		t.Fatal("a current that points outside the cosmovisor directory was accepted")
	}
	// An absolute link inside the directory is read like the relative one.
	if err := os.Remove(layout.Current()); err != nil {
		t.Fatal(err)
	}
	upgrade := filepath.Join(layout.Root(), "upgrades", "v3")
	if err := os.MkdirAll(filepath.Join(upgrade, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(upgrade, layout.Current()); err != nil {
		t.Fatal(err)
	}
	if err := checkCurrentHasVerifier(layout); err == nil {
		t.Fatal("an absolute link to an upgrade with no verifier was accepted")
	}
	if err := os.WriteFile(filepath.Join(upgrade, "bin", "orama-orchard-verifier"), []byte("v"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := checkCurrentHasVerifier(layout); err != nil {
		t.Fatalf("an absolute link to an upgrade with its verifier: %v", err)
	}
}
