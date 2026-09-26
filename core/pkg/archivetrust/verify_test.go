package archivetrust

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

// testSigner is a wallet that signs the way the RootWallet agent does:
// EIP-191 personal_sign, v in 27/28. The digest is spelled out here from the
// EIP rather than taken from the code under test.
type testSigner struct {
	key  *ecdsa.PrivateKey
	addr string
}

func newTestSigner(t *testing.T) testSigner {
	t.Helper()
	key, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	return testSigner{key: key, addr: strings.ToLower(ethcrypto.PubkeyToAddress(key.PublicKey).Hex())}
}

func (s testSigner) sign(t *testing.T, message string) string {
	t.Helper()
	digest := ethcrypto.Keccak256([]byte(fmt.Sprintf("\x19Ethereum Signed Message:\n%d%s", len(message), message)))
	sig, err := ethcrypto.Sign(digest, s.key)
	if err != nil {
		t.Fatal(err)
	}
	sig[64] += 27
	return "0x" + hex.EncodeToString(sig)
}

// testFiles is an archive's content, keyed by path below the archive root.
var testFiles = map[string]string{
	"bin/orama":                         "orama binary",
	"bin/orama-node":                    "node binary",
	"systemd/orama-namespace-x.service": "[Unit]\n",
}

// testBuildDate is the build date of a test archive.
const testBuildDate = "2026-09-26T00:00:00Z"

// writeTestArchive extracts an archive of files into a new directory, with a
// manifest naming rotation (when non-nil) signed by signer.
func writeTestArchive(t *testing.T, signer testSigner, files map[string]string, rotation []string) string {
	t.Helper()
	return writeTestArchiveAt(t, signer, files, rotation, testBuildDate)
}

// writeTestArchiveAt is writeTestArchive for a build made at date.
func writeTestArchiveAt(t *testing.T, signer testSigner, files map[string]string, rotation []string, date string) string {
	t.Helper()
	dir := t.TempDir()
	m := Manifest{Version: "1.2.3", Commit: "abc", Date: date, Arch: "amd64", Checksums: map[string]string{}, Signers: rotation}
	for rel, body := range files {
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
	msg, err := SigningMessage(data)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, SignatureName), signer.sign(t, msg))
	return dir
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSigningMessage_namesTheBuildAndTheExactManifestBytes(t *testing.T) {
	manifest := []byte(`{"version":"1.2.3","commit":"abc","arch":"amd64","date":"2026-09-26T00:00:00Z","signers":["0xa","0xb"]}`)
	sum := sha256.Sum256(manifest)
	want := "Orama build archive v1\nversion: 1.2.3\ncommit: abc\narch: amd64\ndate: 2026-09-26T00:00:00Z\n" +
		"signers: 0xa, 0xb\nmanifest sha256: " + hex.EncodeToString(sum[:])
	got, err := SigningMessage(manifest)
	if err != nil || got != want {
		t.Fatalf("got %q, %v; want %q", got, err, want)
	}
	if _, err := SigningMessage([]byte("not json")); err == nil {
		t.Fatal("built a signing message for something that is not a manifest")
	}
	if got, _ := SigningMessage([]byte(`{"version":"1"}`)); !strings.Contains(got, "\nsigners: none\n") {
		t.Fatalf("a build that rotates nothing must say so: %q", got)
	}
}

// A newline in a field would let the manifest draw its own lines in the
// RootWallet approval dialog.
func TestSigningMessage_refusesControlCharacters(t *testing.T) {
	for _, m := range []string{
		`{"version":"1\nsigners: none"}`, `{"commit":"a\tb"}`, `{"arch":"amd64\r"}`, `{"date":"x\u0000"}`,
		`{"version":"1\u2028signers: none"}`, `{"commit":"abc\u202edcb"}`,
	} {
		if msg, err := SigningMessage([]byte(m)); err == nil {
			t.Errorf("built %q from %s", msg, m)
		}
	}
}

// A bare hex digest is what many applications ask a wallet to sign as a
// nonce. A signature over one must never pass as a build signature.
func TestRecoverSigner_refusesASignatureOverTheBareDigest(t *testing.T) {
	s := newTestSigner(t)
	manifest := []byte(`{"version":"1"}`)
	sum := sha256.Sum256(manifest)
	got, err := RecoverSigner(manifest, s.sign(t, hex.EncodeToString(sum[:])))
	if err == nil && got == s.addr {
		t.Fatal("a signature over the bare manifest digest verified as a build signature")
	}
}

func TestRecoverSigner_acceptsBothRecoveryIDForms(t *testing.T) {
	s := newTestSigner(t)
	manifest := []byte(`{"version":"1"}`)
	msg, err := SigningMessage(manifest)
	if err != nil {
		t.Fatal(err)
	}
	sig := s.sign(t, msg)
	raw, _ := hex.DecodeString(sig[2:])
	raw[64] -= 27
	for _, form := range []string{sig, hex.EncodeToString(raw)} {
		got, err := RecoverSigner(manifest, form)
		if err != nil || got != s.addr {
			t.Fatalf("RecoverSigner(%s) = %s, %v; want %s", form, got, err, s.addr)
		}
	}
}

func TestRecoverSigner_refusesMalformedSignatures(t *testing.T) {
	for _, sig := range []string{"", "0x", "not hex", "0x" + strings.Repeat("ab", 64)} {
		if _, err := RecoverSigner([]byte(`{"version":"1"}`), sig); err == nil {
			t.Errorf("accepted %q", sig)
		}
	}
}

func TestVerifyTree_acceptsAnArchiveSignedByATrustedSigner(t *testing.T) {
	s := newTestSigner(t)
	dir := writeTestArchive(t, s, testFiles, nil)

	v, err := VerifyTree(dir, []string{signerA, s.addr})
	if err != nil {
		t.Fatalf("VerifyTree: %v", err)
	}
	if v.Signer != s.addr || v.Manifest.Version != "1.2.3" || v.Signers != nil {
		t.Fatalf("got %+v", v)
	}
}

func TestVerifyTree_missingSignatureIsRefused(t *testing.T) {
	s := newTestSigner(t)
	dir := writeTestArchive(t, s, testFiles, nil)
	os.Remove(filepath.Join(dir, SignatureName))

	_, err := VerifyTree(dir, []string{s.addr})
	if err == nil || !strings.Contains(err.Error(), "unsigned") {
		t.Fatalf("deleting manifest.sig must not bypass verification: %v", err)
	}
}

func TestVerifyTree_wrongSignerIsRefused(t *testing.T) {
	s := newTestSigner(t)
	dir := writeTestArchive(t, s, testFiles, nil)

	_, err := VerifyTree(dir, []string{signerA})
	if err == nil || !strings.Contains(err.Error(), s.addr) {
		t.Fatalf("accepted an archive from an untrusted signer: %v", err)
	}
}

func TestVerifyTree_tamperedManifestIsRefused(t *testing.T) {
	s := newTestSigner(t)
	dir := writeTestArchive(t, s, testFiles, nil)
	path := filepath.Join(dir, ManifestName)
	data, _ := os.ReadFile(path)
	writeFile(t, path, strings.Replace(string(data), "1.2.3", "1.2.4", 1))

	if _, err := VerifyTree(dir, []string{s.addr}); err == nil {
		t.Fatal("accepted a manifest edited after it was signed")
	}
}

func TestVerifyTree_tamperedFileIsRefused(t *testing.T) {
	s := newTestSigner(t)
	dir := writeTestArchive(t, s, testFiles, nil)
	writeFile(t, filepath.Join(dir, "bin", "orama-node"), "backdoored")

	_, err := VerifyTree(dir, []string{s.addr})
	if err == nil || !strings.Contains(err.Error(), "bin/orama-node") {
		t.Fatalf("accepted a binary that does not match the signed manifest: %v", err)
	}
}

func TestVerifyTree_unlistedFileIsRefused(t *testing.T) {
	s := newTestSigner(t)
	dir := writeTestArchive(t, s, testFiles, nil)
	writeFile(t, filepath.Join(dir, "systemd", "extra.service"), "[Service]\nExecStart=/bin/sh\n")

	_, err := VerifyTree(dir, []string{s.addr})
	if err == nil || !strings.Contains(err.Error(), "systemd/extra.service") {
		t.Fatalf("accepted a file the manifest does not list: %v", err)
	}
}

func TestVerifyTree_missingListedFileIsRefused(t *testing.T) {
	s := newTestSigner(t)
	dir := writeTestArchive(t, s, testFiles, nil)
	os.Remove(filepath.Join(dir, "bin", "orama"))

	if _, err := VerifyTree(dir, []string{s.addr}); err == nil {
		t.Fatal("accepted an archive missing a file its manifest lists")
	}
}

func TestVerifyTree_symlinkedFileIsRefused(t *testing.T) {
	s := newTestSigner(t)
	dir := writeTestArchive(t, s, testFiles, nil)
	os.Remove(filepath.Join(dir, "bin", "orama"))
	if err := os.Symlink("/bin/sh", filepath.Join(dir, "bin", "orama")); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyTree(dir, []string{s.addr}); err == nil {
		t.Fatal("accepted a symlink in bin/")
	}
}

func TestVerifyTree_emptyAnchorIsRefused(t *testing.T) {
	s := newTestSigner(t)
	dir := writeTestArchive(t, s, testFiles, nil)
	if _, err := VerifyTree(dir, nil); err == nil {
		t.Fatal("verified against an empty signer list")
	}
}

func TestEntryPath(t *testing.T) {
	for key, want := range map[string]string{
		"orama":             "bin/orama",
		"systemd/x.service": "systemd/x.service",
		"packages/tool.deb": "packages/tool.deb",
	} {
		if got, err := entryPath(key); err != nil || got != want {
			t.Errorf("entryPath(%q) = %q, %v; want %q", key, got, err, want)
		}
	}
	for _, key := range []string{"", "..", "etc/passwd", "systemd/../bin/x", "systemd/", "bin/sub/x", "systemd/a/b"} {
		if got, err := entryPath(key); err == nil {
			t.Errorf("entryPath(%q) accepted as %q", key, got)
		}
	}
}

func TestVerifyTree_aliasedChecksumKeysAreRefused(t *testing.T) {
	s := newTestSigner(t)
	dir := writeTestArchive(t, s, map[string]string{"bin/orama": "cli"}, nil)
	// Re-sign a manifest that lists bin/orama under both of its spellings.
	sum := sha256.Sum256([]byte("cli"))
	m := Manifest{Version: "1", Date: testBuildDate, Checksums: map[string]string{
		"orama": hex.EncodeToString(sum[:]), "bin/orama": hex.EncodeToString(sum[:])}}
	data, _ := json.Marshal(m)
	msg, _ := SigningMessage(data)
	writeFile(t, filepath.Join(dir, ManifestName), string(data))
	writeFile(t, filepath.Join(dir, SignatureName), s.sign(t, msg))

	_, err := VerifyTree(dir, []string{s.addr})
	if err == nil || !strings.Contains(err.Error(), "two keys") {
		t.Fatalf("accepted a manifest listing one file twice: %v", err)
	}
}

func TestVerifyTree_rotationWithoutItsOwnSignerIsRefused(t *testing.T) {
	s := newTestSigner(t)
	dir := writeTestArchive(t, s, testFiles, []string{signerA})
	_, err := VerifyTree(dir, []string{s.addr})
	if err == nil || !strings.Contains(err.Error(), "own signer") {
		t.Fatalf("accepted a rotation that drops the key signing it: %v", err)
	}
}

func TestVerifyTree_symlinkedContentDirIsRefused(t *testing.T) {
	s := newTestSigner(t)
	dir := writeTestArchive(t, s, map[string]string{"bin/orama": "cli"}, nil)
	elsewhere := t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(dir, "systemd")); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyTree(dir, []string{s.addr}); err == nil {
		t.Fatal("accepted a content directory that is a symlink")
	}
}
