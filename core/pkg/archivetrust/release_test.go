package archivetrust

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/releaseverify"
	"github.com/DeBrosOfficial/network/pkg/releaseverify/releaserepo"
)

// releaseSeams points the adopted root and the staged-release record at a
// temporary directory and returns their paths.
func releaseSeams(t *testing.T) (rootPath, stagedPath string) {
	t.Helper()
	dir := t.TempDir()
	prevRoot, prevStaged := ReleaseRootPath, ReleaseStagedPath
	t.Cleanup(func() { ReleaseRootPath, ReleaseStagedPath = prevRoot, prevStaged })
	ReleaseRootPath = filepath.Join(dir, "release-root.json")
	ReleaseStagedPath = filepath.Join(dir, "release-staged.json")
	return ReleaseRootPath, ReleaseStagedPath
}

// newRoot is a release root valid for a day after testNow.
func newRoot(t *testing.T) []byte {
	t.Helper()
	keys, err := releaserepo.GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	root, err := releaserepo.NewRoot(keys, testNow.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// writeReleaseArchive extracts an archive of testFiles into a new directory
// with a manifest carrying root and signers (either may be nil), signed by
// signer when it is not nil, and dated date.
func writeReleaseArchive(t *testing.T, signer *testSigner, root []byte, signers []string, date string) string {
	t.Helper()
	dir := t.TempDir()
	m := Manifest{Version: "1.2.3", Commit: "abc", Date: date, Arch: "amd64", Checksums: map[string]string{}, Signers: signers}
	if root != nil {
		m.ReleaseRoot = EncodeReleaseRoot(root)
	}
	for rel, body := range testFiles {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256([]byte(body))
		m.Checksums[strings.TrimPrefix(rel, "bin/")] = hex.EncodeToString(sum[:])
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, ManifestName), string(data))
	if signer != nil {
		msg, err := SigningMessage(data)
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(dir, SignatureName), signer.sign(t, msg))
	}
	return dir
}

func manifestDigestOf(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, ManifestName))
	if err != nil {
		t.Fatal(err)
	}
	return ManifestDigest(data)
}

func TestSigningMessage_showsTheReleaseRootOnlyWhenThereIsOne(t *testing.T) {
	root := newRoot(t)
	with := writeReleaseArchive(t, nil, root, nil, testBuildDate)
	without := writeReleaseArchive(t, nil, nil, nil, testBuildDate)
	read := func(dir string) string {
		data, err := os.ReadFile(filepath.Join(dir, ManifestName))
		if err != nil {
			t.Fatal(err)
		}
		msg, err := SigningMessage(data)
		if err != nil {
			t.Fatal(err)
		}
		return msg
	}
	if got := read(with); !strings.Contains(got, "release root sha256: "+releaseverify.RootDigest(root)+"\nmanifest sha256: ") {
		t.Errorf("the message does not show the root before the manifest hash:\n%s", got)
	}
	if got := read(without); strings.Contains(got, "release root") || !strings.Contains(got, "signers: none\nmanifest sha256: ") {
		t.Errorf("a manifest without a root changed the message format:\n%s", got)
	}
}

func TestVerifyAndRotate_adoptsTheReleaseRootASignedBuildCarries(t *testing.T) {
	s := newTestSigner(t)
	anchor, _ := trustingAnchor(t, s.addr)
	rootPath, _ := releaseSeams(t)
	root := newRoot(t)
	dir := writeReleaseArchive(t, &s, root, nil, testBuildDate)

	v, rotated, err := VerifyAndRotate(anchor, dir, testArch)
	if err != nil || rotated || !v.AdoptedRoot {
		t.Fatalf("rotated=%v adopted=%v err=%v", rotated, v != nil && v.AdoptedRoot, err)
	}
	if got, err := os.ReadFile(rootPath); err != nil || string(got) != string(root) {
		t.Fatalf("adopted root = %q, %v", got, err)
	}
	if at, err := ReadRotationMark(anchor); err != nil || at.Format(time.RFC3339) != testBuildDate {
		t.Fatalf("mark = %v, %v", at, err)
	}
	v, _, err = VerifyAndRotate(anchor, dir, testArch)
	if err != nil || v.AdoptedRoot {
		t.Fatalf("the same archive again adopted again: %v, %v", v.AdoptedRoot, err)
	}
}

func TestVerifyAndRotate_anOlderBuildCannotPutBackAReplacedRoot(t *testing.T) {
	s := newTestSigner(t)
	anchor, _ := trustingAnchor(t, s.addr)
	rootPath, _ := releaseSeams(t)
	oldRoot, newerRoot := newRoot(t), newRoot(t)
	older := writeReleaseArchive(t, &s, oldRoot, nil, "2026-09-01T00:00:00Z")
	newer := writeReleaseArchive(t, &s, newerRoot, nil, "2026-09-02T00:00:00Z")
	for _, dir := range []string{older, newer} {
		if _, _, err := VerifyAndRotate(anchor, dir, testArch); err != nil {
			t.Fatal(err)
		}
	}
	_, _, err := VerifyAndRotate(anchor, older, testArch)
	if err == nil || !strings.Contains(err.Error(), "replayed") {
		t.Fatalf("an old build replaced the root: %v", err)
	}
	if got, _ := os.ReadFile(rootPath); string(got) != string(newerRoot) {
		t.Fatal("the adopted root changed after a refused replay")
	}
}

func TestVerifyTree_aSignedBuildCarryingAnInvalidRootIsRefused(t *testing.T) {
	s := newTestSigner(t)
	releaseSeams(t)
	prev := now
	t.Cleanup(func() { now = prev })
	now = func() time.Time { return testNow }
	for name, root := range map[string][]byte{"garbage": []byte("not a root"), "expired": expiredRoot(t)} {
		dir := writeReleaseArchive(t, &s, root, nil, testBuildDate)
		if _, err := VerifyTree(dir, []string{s.addr}); err == nil || !strings.Contains(err.Error(), "release_root") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func expiredRoot(t *testing.T) []byte {
	t.Helper()
	keys, err := releaserepo.GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	root, err := releaserepo.NewRoot(keys, testNow.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// stageUnsigned adopts root and records an endorsement for dir's manifest,
// as `stage-archive --release-only` does.
func stageUnsigned(t *testing.T, dir string, root []byte) {
	t.Helper()
	if _, err := releaseverify.AdoptRoot(ReleaseRootPath, root, testNow); err != nil {
		t.Fatal(err)
	}
	err := releaseverify.RecordStaged(ReleaseStagedPath, releaseverify.Endorsement{
		ManifestSHA256: manifestDigestOf(t, dir), RootSHA256: releaseverify.RootDigest(root),
		Target: "stable/orama-1.2.3-linux-amd64.tar.gz", SnapshotVersion: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestVerify_anUnsignedBuildStagedThroughTheReleaseRootVerifies(t *testing.T) {
	anchor, _ := trustingAnchor(t, signerA)
	releaseSeams(t)
	root := newRoot(t)
	dir := writeReleaseArchive(t, nil, nil, nil, testBuildDate)
	stageUnsigned(t, dir, root)

	v, err := Verify(anchor, dir, testArch)
	if err != nil {
		t.Fatalf("a staged release was refused: %v", err)
	}
	if !strings.HasPrefix(v.Signer, "release-root:"+releaseverify.RootDigest(root)[:12]) {
		t.Errorf("signer = %q", v.Signer)
	}
	if _, _, err := VerifyAndRotate(anchor, dir, testArch); err != nil {
		t.Fatalf("the install path refused a staged release: %v", err)
	}
}

func TestVerify_anUnsignedBuildThatWasNotStagedIsRefusedAsUnsigned(t *testing.T) {
	anchor, _ := trustingAnchor(t, signerA)
	releaseSeams(t)
	dir := writeReleaseArchive(t, nil, nil, nil, testBuildDate)
	_, err := Verify(anchor, dir, testArch)
	if err == nil || !strings.Contains(err.Error(), "unsigned") {
		t.Fatalf("err = %v, want the unsigned refusal", err)
	}
}

func TestVerify_aStagedEndorsementDoesNotSurviveAReplacedRoot(t *testing.T) {
	anchor, _ := trustingAnchor(t, signerA)
	releaseSeams(t)
	dir := writeReleaseArchive(t, nil, nil, nil, testBuildDate)
	stageUnsigned(t, dir, newRoot(t))
	if _, err := releaseverify.AdoptRoot(ReleaseRootPath, newRoot(t), testNow); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(anchor, dir, testArch); err == nil {
		t.Fatal("an endorsement given under another root was honoured")
	}
}

func TestVerify_aStagedBuildIsStillCheckedFileByFileAndForItsArch(t *testing.T) {
	anchor, _ := trustingAnchor(t, signerA)
	releaseSeams(t)
	root := newRoot(t)
	dir := writeReleaseArchive(t, nil, nil, nil, testBuildDate)
	stageUnsigned(t, dir, root)
	if _, err := Verify(anchor, dir, "arm64"); err == nil {
		t.Error("a staged amd64 build verified for arm64")
	}
	writeFile(t, filepath.Join(dir, "bin", "orama"), "swapped after staging")
	if _, err := Verify(anchor, dir, testArch); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Errorf("a file changed after staging: %v", err)
	}
}

func TestVerify_aStagedBuildCannotChangeWhoIsTrusted(t *testing.T) {
	anchor, _ := trustingAnchor(t, signerA)
	releaseSeams(t)
	root := newRoot(t)
	for name, dir := range map[string]string{
		"signers": writeReleaseArchive(t, nil, nil, []string{signerA, signerB}, testBuildDate),
		"root":    writeReleaseArchive(t, nil, newRoot(t), nil, testBuildDate),
	} {
		stageUnsigned(t, dir, root)
		if _, err := Verify(anchor, dir, testArch); err == nil || !strings.Contains(err.Error(), "only a build signed") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestVerify_aSignatureIsNeverBypassedByAnEndorsement(t *testing.T) {
	anchor, _ := trustingAnchor(t, signerA)
	releaseSeams(t)
	attacker := newTestSigner(t)
	dir := writeReleaseArchive(t, &attacker, nil, nil, testBuildDate)
	stageUnsigned(t, dir, newRoot(t))
	if _, err := Verify(anchor, dir, testArch); err == nil {
		t.Fatal("a build signed by an untrusted wallet verified because its manifest was endorsed")
	}
}

func TestVerifyUnsignedTree_refusesATreeThatHasASignature(t *testing.T) {
	s := newTestSigner(t)
	dir := writeReleaseArchive(t, &s, nil, nil, testBuildDate)
	if _, _, err := VerifyUnsignedTree(dir, testArch); err == nil {
		t.Fatal("a signed tree was treated as a release")
	}
	unsigned := writeReleaseArchive(t, nil, nil, nil, testBuildDate)
	v, manifest, err := VerifyUnsignedTree(unsigned, testArch)
	if err != nil || v.Manifest.Version != "1.2.3" || ManifestDigest(manifest) != manifestDigestOf(t, unsigned) {
		t.Fatalf("unsigned tree: %v, %v", v, err)
	}
}
