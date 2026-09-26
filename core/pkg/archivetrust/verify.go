package archivetrust

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/DeBrosOfficial/network/pkg/rootfs"
	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

// Manifest describes the contents of a build archive: `orama build` writes it
// as manifest.json and signs it, and nodes verify it.
type Manifest struct {
	Version   string            `json:"version"`
	Commit    string            `json:"commit"`
	Date      string            `json:"date"` // RFC 3339, UTC
	Arch      string            `json:"arch"`
	Checksums map[string]string `json:"checksums"` // file -> sha256; see entryPath
	// Signers, when present, rotates the trust anchor: a node that verifies
	// this manifest against its current anchor then trusts exactly these. The
	// list must include the address that signed the manifest.
	Signers []string `json:"signers,omitempty"`
}

// Names inside a build archive, and under /opt/orama once it is extracted.
const (
	ManifestName  = "manifest.json"
	SignatureName = "manifest.sig"
)

// ContentDirs hold every file an archive installs besides the manifest and
// its signature. A manifest checksum key without a slash names a file in bin/
// (the form every archive has used); any other file is keyed by its path.
var ContentDirs = []string{"bin", "systemd", "packages"}

// OwnedPaths are the entries an archive installs under /opt/orama. They are
// replaced as a whole, so nothing from an older build survives next to a newer
// one — above all not a manifest.sig that no longer matches the manifest
// beside it. /opt/orama also holds the node's data (.orama/), which is never
// touched.
var OwnedPaths = []string{ManifestName, SignatureName, "bin", "systemd", "packages"}

const (
	// binDir is where a slash-less checksum key points.
	binDir = "bin"
	// manifestLimit and signatureLimit bound the reads of the two small
	// files; a manifest is a few kilobytes of checksums.
	manifestLimit  = 1 << 20
	signatureLimit = 1 << 10
	// MaxFileBytes bounds one archived file; the largest binary is well under
	// it.
	MaxFileBytes = 512 << 20
	// evmSignatureLen is r, s and v.
	evmSignatureLen = 65
	// evmRecoveryIDOffset is added to v by signers that use the legacy 27/28
	// form rather than 0/1.
	evmRecoveryIDOffset = 27
	// personalSignPrefix is EIP-191's prefix for a personal_sign message.
	personalSignPrefix = "\x19Ethereum Signed Message:\n"
	// signingDomain opens every message a build signature covers, so a
	// signature over a bare hex string that some other application asked the
	// same wallet for can never pass as a build signature.
	signingDomain = "Orama build archive v1"
)

// Verified is an archive whose signature recovered to a trusted signer and
// whose files all match the manifest that signature covers.
type Verified struct {
	Manifest *Manifest
	// Signer is the lowercase address that signed the manifest.
	Signer string
	// Signers is the rotation the manifest carries, normalized; nil when it
	// names none.
	Signers []string
}

// SigningMessage is the text a signer signs for manifestJSON: a fixed domain
// line, the build it names — including the build date and the signer list it
// would rotate the nodes to, so the approval dialog shows a change of who is
// trusted — and the SHA-256 of manifest.json exactly as it sits in the
// archive. A field with a character that is not printable is refused: it could
// fake a line of the dialog.
func SigningMessage(manifestJSON []byte) (string, error) {
	var m Manifest
	if err := json.Unmarshal(manifestJSON, &m); err != nil {
		return "", fmt.Errorf("%s is not a build manifest: %w", ManifestName, err)
	}
	for name, value := range map[string]string{"version": m.Version, "commit": m.Commit, "arch": m.Arch, "date": m.Date} {
		if strings.ContainsFunc(value, notPrintable) {
			return "", fmt.Errorf("the manifest's %s %q contains a character that is not printable", name, value)
		}
	}
	signers := "none"
	if m.Signers != nil {
		signers = strings.Join(m.Signers, ", ")
		if strings.ContainsFunc(signers, notPrintable) {
			return "", fmt.Errorf("the manifest's signer list contains a character that is not printable")
		}
	}
	sum := sha256.Sum256(manifestJSON)
	return fmt.Sprintf("%s\nversion: %s\ncommit: %s\narch: %s\ndate: %s\nsigners: %s\nmanifest sha256: %s",
		signingDomain, m.Version, m.Commit, m.Arch, m.Date, signers, hex.EncodeToString(sum[:])), nil
}

// RecoverSigner returns the lowercase address whose EIP-191 personal_sign
// signature over SigningMessage(manifestJSON) signature is. `orama build`
// signs through the RootWallet agent's /v1/wallet/sign, which produces exactly
// this.
func RecoverSigner(manifestJSON []byte, signature string) (string, error) {
	text, err := SigningMessage(manifestJSON)
	if err != nil {
		return "", err
	}
	msg := []byte(text)
	hash := ethcrypto.Keccak256([]byte(personalSignPrefix+strconv.Itoa(len(msg))), msg)

	sigHex := strings.TrimSpace(signature)
	sigHex = strings.TrimPrefix(strings.TrimPrefix(sigHex, "0x"), "0X")
	sig, err := hex.DecodeString(sigHex)
	if err != nil || len(sig) != evmSignatureLen {
		return "", fmt.Errorf("not a %d-byte hex EVM signature", evmSignatureLen)
	}
	if sig[evmSignatureLen-1] >= evmRecoveryIDOffset {
		sig[evmSignatureLen-1] -= evmRecoveryIDOffset
	}
	pub, err := ethcrypto.SigToPub(hash, sig)
	if err != nil {
		return "", fmt.Errorf("recover the signer: %w", err)
	}
	return strings.ToLower(ethcrypto.PubkeyToAddress(*pub).Hex()), nil
}

// VerifyTree checks the extracted archive in dir: manifest.sig must be a
// signature of manifest.json by one of trusted, a signer list in it must name
// that signer, and every file in bin/, systemd/ and packages/ must be listed
// in the manifest with the checksum it has — no more files, no fewer. There
// is no unsigned mode.
func VerifyTree(dir string, trusted []string) (*Verified, error) {
	if len(trusted) == 0 {
		return nil, fmt.Errorf("%w: the trusted signer list is empty", ErrNoAnchor)
	}
	manifestJSON, signer, err := verifySignature(dir, trusted)
	if err != nil {
		return nil, err
	}
	var manifest Manifest
	if err := json.Unmarshal(manifestJSON, &manifest); err != nil {
		return nil, fmt.Errorf("parse the signed manifest: %w", err)
	}
	rotation, err := manifestRotation(&manifest, signer)
	if err != nil {
		return nil, err
	}
	if err := verifyContents(dir, manifest.Checksums); err != nil {
		return nil, err
	}
	return &Verified{Manifest: &manifest, Signer: signer, Signers: rotation}, nil
}

// notPrintable reports a character that could draw something other than
// itself in the approval dialog: controls, line and paragraph separators, and
// bidirectional overrides (which unicode.IsPrint also refuses).
func notPrintable(r rune) bool {
	return !unicode.IsPrint(r) || unicode.In(r, unicode.Bidi_Control)
}

// VerifyIntegrity checks the archive in dir against whoever signed it: the
// signature is valid and every file matches the signed manifest. It says
// nothing about whether that signer is trusted — a joining node runs it before
// it has the cluster's signers, so the only thing left to fail once the join
// has spent the invite is the signer's membership.
func VerifyIntegrity(dir string) (*Verified, error) {
	manifestJSON, err := rootfs.At(dir).ReadFile(filepath.Join(dir, ManifestName), manifestLimit)
	if err != nil {
		return nil, fmt.Errorf("read the archive manifest: %w", err)
	}
	sig, err := rootfs.At(dir).ReadFile(filepath.Join(dir, SignatureName), signatureLimit)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("the archive in %s is unsigned (no %s)", dir, SignatureName)
	}
	if err != nil {
		return nil, fmt.Errorf("read the archive signature: %w", err)
	}
	signer, err := RecoverSigner(manifestJSON, string(sig))
	if err != nil {
		return nil, fmt.Errorf("%s is not a valid signature of %s: %w", SignatureName, ManifestName, err)
	}
	return VerifyTree(dir, []string{signer})
}

// verifySignature reads dir's manifest and signature and returns the manifest
// bytes and the trusted signer they recover to.
func verifySignature(dir string, trusted []string) ([]byte, string, error) {
	root := rootfs.At(dir)
	manifestJSON, err := root.ReadFile(filepath.Join(dir, ManifestName), manifestLimit)
	if err != nil {
		return nil, "", fmt.Errorf("read the archive manifest: %w", err)
	}
	sig, err := root.ReadFile(filepath.Join(dir, SignatureName), signatureLimit)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, "", fmt.Errorf("the archive in %s is unsigned (no %s); nodes install only signed builds — "+
			"build it with `orama build`, which signs with your RootWallet", dir, SignatureName)
	}
	if err != nil {
		return nil, "", fmt.Errorf("read the archive signature: %w", err)
	}
	signer, err := RecoverSigner(manifestJSON, string(sig))
	if err != nil {
		return nil, "", fmt.Errorf("%s is not a valid signature of %s: %w", SignatureName, ManifestName, err)
	}
	if !slices.Contains(trusted, signer) {
		return nil, "", fmt.Errorf("the archive is signed by %s, which this node does not trust (it trusts %s); "+
			"sign the build with one of those wallets", signer, strings.Join(trusted, ", "))
	}
	return manifestJSON, signer, nil
}

// manifestRotation validates the signer list a manifest carries. It must name
// the manifest's own signer: the archive then still verifies after the node
// rotates to that list, so re-running an interrupted upgrade, or a join
// through a node that already rotated, installs the same build. Retiring a
// key therefore takes two builds — the old key signs a list with both, the new
// key then signs a list without the old one.
func manifestRotation(m *Manifest, signer string) ([]string, error) {
	if m.Signers == nil {
		return nil, nil
	}
	rotation, err := NormalizeSigners(m.Signers)
	if err != nil {
		return nil, fmt.Errorf("the signed manifest's signer list is invalid: %w", err)
	}
	if !slices.Contains(rotation, signer) {
		return nil, fmt.Errorf("the signed manifest rotates to %s, which leaves out its own signer %s; a "+
			"rotation must include the key that signs it (retire a key with a second build signed by a new one)",
			strings.Join(rotation, ", "), signer)
	}
	return rotation, nil
}

// verifyContents checks the files in dir against checksums.
func verifyContents(dir string, checksums map[string]string) error {
	if len(checksums) == 0 {
		return fmt.Errorf("the signed manifest lists no files")
	}
	listed := make(map[string]string, len(checksums))
	for key, sum := range checksums {
		rel, err := entryPath(key)
		if err != nil {
			return fmt.Errorf("the signed manifest lists %q: %w", key, err)
		}
		if _, dup := listed[rel]; dup {
			return fmt.Errorf("the signed manifest lists %s under two keys", rel)
		}
		listed[rel] = strings.ToLower(sum)
	}
	if err := refuseUnlistedFiles(dir, listed); err != nil {
		return err
	}
	for rel, want := range listed {
		got, err := hashFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			return fmt.Errorf("the signed manifest lists %s: %w", rel, err)
		}
		if got != want {
			return fmt.Errorf("%s does not match the signed manifest (sha256 %s, manifest says %s): "+
				"the archive was modified after it was signed", rel, got, want)
		}
	}
	return nil
}

// hashFile streams the SHA-256 of the regular file at path, refusing a
// symlink there and a file over MaxFileBytes.
func hashFile(path string) (string, error) {
	f, info, err := openNoFollow(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if info.Size() > MaxFileBytes {
		return "", fmt.Errorf("%d bytes, over the %d-byte limit for one archived file", info.Size(), MaxFileBytes)
	}
	h := sha256.New()
	if _, err := io.Copy(h, io.LimitReader(f, MaxFileBytes+1)); err != nil {
		return "", fmt.Errorf("read: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// refuseUnlistedFiles fails on a content directory that is not a real
// directory, and on any entry in one that the manifest does not list or that
// is not a regular file.
func refuseUnlistedFiles(dir string, listed map[string]string) error {
	for _, sub := range ContentDirs {
		info, err := lstat(filepath.Join(dir, sub))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("stat %s in the archive: %w", sub, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("%s in the archive is not a directory (%s)", sub, info.Mode().Type())
		}
		entries, err := os.ReadDir(filepath.Join(dir, sub))
		if err != nil {
			return fmt.Errorf("list %s in the archive: %w", sub, err)
		}
		for _, e := range entries {
			rel := sub + "/" + e.Name()
			if !e.Type().IsRegular() {
				return fmt.Errorf("%s in the archive is not a regular file (%s)", rel, e.Type())
			}
			if _, ok := listed[rel]; !ok {
				return fmt.Errorf("%s is in the archive but not in its signed manifest", rel)
			}
		}
	}
	return nil
}

// entryPath is the slash-separated path, relative to the archive root, of the
// file a manifest checksum key names.
func entryPath(key string) (string, error) {
	dir, name, found := strings.Cut(key, "/")
	if !found {
		dir, name = binDir, key
	}
	if !slices.Contains(ContentDirs, dir) {
		return "", fmt.Errorf("not in %s", strings.Join(ContentDirs, ", "))
	}
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00") {
		return "", fmt.Errorf("not a plain file name")
	}
	return dir + "/" + name, nil
}
