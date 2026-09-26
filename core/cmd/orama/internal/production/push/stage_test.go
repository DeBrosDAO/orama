package push

import (
	"archive/tar"
	"compress/gzip"
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/archivetrust"
	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

type tarEntry struct {
	name     string
	body     string
	typeflag byte // 0: what `orama build` writes (no type set)
	link     string
}

// signedEntries is an archive's entries with a manifest over files, signed by
// key the way the RootWallet agent signs (EIP-191, v in 27/28).
func signedEntries(t *testing.T, key *ecdsa.PrivateKey, files map[string]string) []tarEntry {
	t.Helper()
	m := archivetrust.Manifest{Version: "2.0.0", Commit: "def", Date: "2026-09-26T00:00:00Z", Arch: "amd64", Checksums: map[string]string{}}
	entries := []tarEntry{{name: "bin/", typeflag: tar.TypeDir}, {name: "systemd/", typeflag: tar.TypeDir}}
	for name, body := range files {
		sum := sha256.Sum256([]byte(body))
		m.Checksums[strings.TrimPrefix(name, "bin/")] = hex.EncodeToString(sum[:])
		entries = append(entries, tarEntry{name: name, body: body})
	}
	manifest, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	msg, err := archivetrust.SigningMessage(manifest)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := ethcrypto.Sign(ethcrypto.Keccak256([]byte(fmt.Sprintf("\x19Ethereum Signed Message:\n%d%s", len(msg), msg))), key)
	if err != nil {
		t.Fatal(err)
	}
	sig[64] += 27
	return append(entries,
		tarEntry{name: archivetrust.ManifestName, body: string(manifest)},
		tarEntry{name: archivetrust.SignatureName, body: "0x" + hex.EncodeToString(sig)})
}

func writeTarball(t *testing.T, entries []tarEntry) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orama.tar.gz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Mode: 0o755, Size: int64(len(e.body)), Typeflag: e.typeflag, Linkname: e.link}
		if e.typeflag != 0 && e.typeflag != tar.TypeReg {
			hdr.Size = 0
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if hdr.Size > 0 {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	tw.Close()
	gz.Close()
	f.Close()
	return path
}

func newSigner(t *testing.T) (*ecdsa.PrivateKey, string) {
	t.Helper()
	key, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	return key, strings.ToLower(ethcrypto.PubkeyToAddress(key.PublicKey).Hex())
}

// installedNode is a /opt/orama holding an older build and the node's data.
func installedNode(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	for rel, body := range map[string]string{
		"bin/orama":            "old cli",
		"bin/retired-binary":   "old",
		"manifest.json":        "{}",
		"manifest.sig":         "0xold",
		".orama/data/node.key": "node identity",
	} {
		p := filepath.Join(base, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return base
}

// testTarget stages into base for a node that trusts signers (none: no
// anchor), recording what stage-archive asks of the anchor and of chown.
type testTarget struct {
	stageTarget
	anchor  []string
	created [][]string
	chowned []string
}

func trusting(base string, signers ...string) *testTarget {
	tt := &testTarget{anchor: signers}
	tt.stageTarget = stageTarget{
		base: base,
		readSigners: func() ([]string, error) {
			if tt.anchor == nil {
				return nil, fmt.Errorf("%w: missing", archivetrust.ErrNoAnchor)
			}
			return tt.anchor, nil
		},
		verify: func(dir string) (*archivetrust.Verified, error) {
			v, err := archivetrust.VerifyTree(dir, tt.anchor)
			if err == nil && v.Manifest.Arch != tt.arch {
				return nil, fmt.Errorf("built for %s", v.Manifest.Arch)
			}
			return v, err
		},
		createSigners: func(s []string) error {
			tt.created = append(tt.created, s)
			tt.anchor = s
			return nil
		},
		chownBin: func(p string) error { tt.chowned = append(tt.chowned, p); return nil },
		arch:     "amd64",
	}
	return tt
}

var newBuild = map[string]string{"bin/orama": "new cli", "systemd/orama-namespace-x.service": "[Unit]\n"}

func TestStage_verifiedArchiveReplacesWhatAnArchiveOwns(t *testing.T) {
	key, addr := newSigner(t)
	base := installedNode(t)
	archive := writeTarball(t, signedEntries(t, key, newBuild))

	if err := stageArchive(trusting(base, addr).stageTarget, StageOptions{Archive: archive}); err != nil {
		t.Fatalf("stage: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(base, "bin", "orama")); string(b) != "new cli" {
		t.Errorf("bin/orama = %q", b)
	}
	if _, err := os.Stat(filepath.Join(base, "bin", "retired-binary")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a binary from the older build survived beside the new one: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(base, ".orama", "data", "node.key")); string(b) != "node identity" {
		t.Errorf("the node's data was touched: %q", b)
	}
	if v, err := archivetrust.VerifyTree(base, []string{addr}); err != nil {
		t.Errorf("what was put in place does not verify: %v", err)
	} else if v.Manifest.Version != "2.0.0" {
		t.Errorf("installed manifest %+v", v.Manifest)
	}
	leftovers, _ := filepath.Glob(filepath.Join(base, stagingPrefix+"*"))
	if len(leftovers) != 0 {
		t.Errorf("staging directories left behind: %v", leftovers)
	}
}

// assertUntouched fails if the older build under base was changed.
func assertUntouched(t *testing.T, base string) {
	t.Helper()
	if b, _ := os.ReadFile(filepath.Join(base, "bin", "orama")); string(b) != "old cli" {
		t.Fatalf("a refused archive replaced bin/orama with %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(base, "manifest.sig")); string(b) != "0xold" {
		t.Fatalf("a refused archive replaced manifest.sig")
	}
}

func TestStage_untrustedSignerChangesNothing(t *testing.T) {
	key, _ := newSigner(t)
	_, trusted := newSigner(t)
	base := installedNode(t)
	archive := writeTarball(t, signedEntries(t, key, newBuild))

	err := stageArchive(trusting(base, trusted).stageTarget, StageOptions{Archive: archive})
	if err == nil || !strings.Contains(err.Error(), "does not trust") {
		t.Fatalf("staged an archive from an untrusted signer: %v", err)
	}
	assertUntouched(t, base)
}

func TestStage_unsignedArchiveChangesNothing(t *testing.T) {
	key, addr := newSigner(t)
	base := installedNode(t)
	entries := signedEntries(t, key, newBuild)
	archive := writeTarball(t, entries[:len(entries)-1]) // without manifest.sig

	if err := stageArchive(trusting(base, addr).stageTarget, StageOptions{Archive: archive}); err == nil {
		t.Fatal("staged an unsigned archive")
	}
	assertUntouched(t, base)
}

func TestStage_tamperedBinaryChangesNothing(t *testing.T) {
	key, addr := newSigner(t)
	base := installedNode(t)
	entries := signedEntries(t, key, newBuild)
	for i := range entries {
		if entries[i].name == "bin/orama" {
			entries[i].body = "backdoored"
		}
	}
	if err := stageArchive(trusting(base, addr).stageTarget, StageOptions{Archive: writeTarball(t, entries)}); err == nil {
		t.Fatal("staged a binary that does not match the signed manifest")
	}
	assertUntouched(t, base)
}

func TestStage_nodeWithoutAnAnchorChangesNothing(t *testing.T) {
	key, _ := newSigner(t)
	base := installedNode(t)
	err := stageArchive(trusting(base).stageTarget, StageOptions{Archive: writeTarball(t, signedEntries(t, key, newBuild))})
	if !errors.Is(err, archivetrust.ErrNoAnchor) {
		t.Fatalf("got %v", err)
	}
	assertUntouched(t, base)
}

func TestStage_trustSignersCreatesTheAnchorOnlyAfterTheArchiveVerifies(t *testing.T) {
	key, addr := newSigner(t)
	base := installedNode(t)
	tt := trusting(base)
	opts := StageOptions{Archive: writeTarball(t, signedEntries(t, key, newBuild)), TrustSigners: []string{addr}}
	if err := stageArchive(tt.stageTarget, opts); err != nil {
		t.Fatalf("stage: %v", err)
	}
	if len(tt.created) != 1 || !slices.Equal(tt.created[0], []string{addr}) {
		t.Fatalf("anchor writes = %v", tt.created)
	}
}

// A mistyped --trust-signers address must fail the push and leave no anchor
// behind: CreateAnchorIfMissing would never let the operator correct it.
func TestStage_mistypedTrustSignersWritesNoAnchor(t *testing.T) {
	key, _ := newSigner(t)
	base := installedNode(t)
	tt := trusting(base)
	opts := StageOptions{Archive: writeTarball(t, signedEntries(t, key, newBuild)),
		TrustSigners: []string{"0x1111111111111111111111111111111111111111"}}
	if err := stageArchive(tt.stageTarget, opts); err == nil {
		t.Fatal("staged an archive not signed by the --trust-signers address")
	}
	if len(tt.created) != 0 {
		t.Fatalf("an anchor was written for an archive that did not verify: %v", tt.created)
	}
	assertUntouched(t, base)
}

func TestStage_trustSignersCannotChangeAnExistingAnchor(t *testing.T) {
	key, addr := newSigner(t)
	base := installedNode(t)
	tt := trusting(base, addr)
	opts := StageOptions{Archive: writeTarball(t, signedEntries(t, key, newBuild)),
		TrustSigners: []string{"0x1111111111111111111111111111111111111111"}}
	if err := stageArchive(tt.stageTarget, opts); err == nil || !strings.Contains(err.Error(), "only creates a missing anchor") {
		t.Fatalf("got %v", err)
	}
	assertUntouched(t, base)
}

func TestStage_binIsRootOramaAndNotWorldReadable(t *testing.T) {
	key, addr := newSigner(t)
	base := installedNode(t)
	tt := trusting(base, addr)
	if err := stageArchive(tt.stageTarget, StageOptions{Archive: writeTarball(t, signedEntries(t, key, newBuild))}); err != nil {
		t.Fatalf("stage: %v", err)
	}
	for _, p := range []string{"bin", "bin/orama"} {
		info, err := os.Stat(filepath.Join(base, p))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != binPerm {
			t.Errorf("%s has mode %#o, want %#o", p, info.Mode().Perm(), binPerm)
		}
	}
	if len(tt.chowned) != 2 {
		t.Errorf("bin and its binary were not given to root:orama: %v", tt.chowned)
	}
}

func TestStage_wrongArchitectureChangesNothing(t *testing.T) {
	key, addr := newSigner(t)
	base := installedNode(t)
	tt := trusting(base, addr)
	tt.arch = "arm64"
	if err := stageArchive(tt.stageTarget, StageOptions{Archive: writeTarball(t, signedEntries(t, key, newBuild))}); err == nil {
		t.Fatal("staged an amd64 build on an arm64 node")
	}
	assertUntouched(t, base)
}

func TestStage_removesStagingLeftByAnInterruptedRun(t *testing.T) {
	key, addr := newSigner(t)
	base := installedNode(t)
	leftover := filepath.Join(base, stagingPrefix+"crashed")
	if err := os.MkdirAll(filepath.Join(leftover, "new", "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	cliLeftover := filepath.Join(base, SetupCLIPrefix+"Crashed1")
	if err := os.MkdirAll(filepath.Join(cliLeftover, "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := stageArchive(trusting(base, addr).stageTarget, StageOptions{Archive: writeTarball(t, signedEntries(t, key, newBuild))}); err != nil {
		t.Fatalf("stage: %v", err)
	}
	for _, dir := range []string{leftover, cliLeftover} {
		if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("the leftover %s is still there: %v", dir, err)
		}
	}
}

func TestSwapArchive_failureRestoresThePreviousBuild(t *testing.T) {
	key, addr := newSigner(t)
	base := installedNode(t)
	archive := writeTarball(t, signedEntries(t, key, newBuild))

	calls := 0
	prev := renameEntry
	t.Cleanup(func() { renameEntry = prev })
	renameEntry = func(from, to string) error {
		calls++
		// Three renames move the previous build aside (manifest.json,
		// manifest.sig, bin); the sixth is the third new path moving in.
		if calls == 6 {
			return errors.New("no space left on device")
		}
		return os.Rename(from, to)
	}
	if err := stageArchive(trusting(base, addr).stageTarget, StageOptions{Archive: archive}); err == nil {
		t.Fatal("a failed swap was reported as a success")
	}
	assertUntouched(t, base)
	if b, _ := os.ReadFile(filepath.Join(base, "bin", "retired-binary")); string(b) != "old" {
		t.Fatal("the previous build's binaries were not all restored")
	}
	if _, err := os.Stat(filepath.Join(base, "systemd")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a new path was left in place after the rollback")
	}
}

func TestStageCommand_runsTheInstalledCLI(t *testing.T) {
	const a, b = "0x1111111111111111111111111111111111111111", "0x2222222222222222222222222222222222222222"
	if got, want := stageCommand("sudo ", "/tmp/x.tar.gz", nil), "sudo /usr/local/bin/orama node stage-archive --archive /tmp/x.tar.gz"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got, want := stageCommand("", "/tmp/x.tar.gz", []string{a, b}), "/usr/local/bin/orama node stage-archive --archive /tmp/x.tar.gz --trust-signers "+a+","+b; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestValidate_normalizesTrustSigners(t *testing.T) {
	f := &Flags{Env: "devnet", Archive: "x", TrustSigners: []string{"0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}}
	if err := f.validate(); err != nil || f.TrustSigners[0] != strings.ToLower(f.TrustSigners[0]) {
		t.Fatalf("validate: %v, %v", err, f.TrustSigners)
	}
	bad := &Flags{Env: "devnet", Archive: "x", TrustSigners: []string{"0x12; rm -rf /"}}
	if err := bad.validate(); err == nil {
		t.Fatal("accepted a --trust-signers value that is not an address (it goes into a remote shell command)")
	}
}

func TestMakeUploadDir_acceptsOnlyWhatMktempMakes(t *testing.T) {
	dir, err := makeUploadDir(func(cmd string) (string, error) {
		if cmd != "mktemp -d "+uploadDirTemplate {
			t.Fatalf("ran %q", cmd)
		}
		return "/tmp/orama-push.Ab12Cd34\n", nil
	})
	if err != nil || dir != "/tmp/orama-push.Ab12Cd34" {
		t.Fatalf("got %q, %v", dir, err)
	}
	for _, out := range []string{"/tmp/x; reboot", "/etc", "", "/tmp/orama-push.Ab12Cd34/../x"} {
		if _, err := makeUploadDir(func(string) (string, error) { return out, nil }); err == nil {
			t.Errorf("accepted %q as the upload directory; it goes into root commands", out)
		}
	}
	if _, err := makeUploadDir(func(string) (string, error) { return "", errors.New("ssh: connect refused") }); err == nil {
		t.Error("a failed mktemp was not an error")
	}
}

func TestStageAndRemove_removesTheUploadEvenWhenTheStageFails(t *testing.T) {
	got := stageAndRemove("sudo ", "/tmp/orama-push.Ab12Cd34", nil)
	want := "sudo /usr/local/bin/orama node stage-archive --archive /tmp/orama-push.Ab12Cd34/archive.tar.gz; rc=$?; " +
		"sudo rm -rf /tmp/orama-push.Ab12Cd34; exit $rc"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
