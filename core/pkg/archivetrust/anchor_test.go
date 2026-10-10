package archivetrust

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/rootfs"
)

const (
	signerA = "0x1111111111111111111111111111111111111111"
	signerB = "0x2222222222222222222222222222222222222222"
)

// anchorSeams makes the anchor code usable by an unprivileged test: the file
// must be owned by whoever creates files in dir, and chown to root is
// recorded instead of performed. It returns the paths chown was asked for.
func anchorSeams(t *testing.T, dir string) *[]string {
	t.Helper()
	probe := filepath.Join(dir, ".owner-probe")
	if err := os.WriteFile(probe, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(probe)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := ownerOf(info)
	if err != nil {
		t.Fatal(err)
	}
	os.Remove(probe)

	prevOwner, prevChown := anchorOwner, chownAnchor
	t.Cleanup(func() { anchorOwner, chownAnchor = prevOwner, prevChown })
	anchorOwner = owner
	var chowned []string
	chownAnchor = func(_ rootfs.Root, path string) error {
		chowned = append(chowned, path)
		return nil
	}
	return &chowned
}

func TestParseAnchor_acceptsOneLowercaseAddressPerLine(t *testing.T) {
	got, err := ParseAnchor([]byte(signerA + "\n" + signerB + "\n"))
	if err != nil {
		t.Fatalf("ParseAnchor: %v", err)
	}
	if !slices.Equal(got, []string{signerA, signerB}) {
		t.Fatalf("got %v", got)
	}
}

func TestParseAnchor_refusesAnythingElse(t *testing.T) {
	for name, data := range map[string]string{
		"empty":           "",
		"only a newline":  "\n",
		"uppercase":       strings.ToUpper(signerA[2:]) + "\n",
		"checksum case":   "0xAbCd111111111111111111111111111111111111\n",
		"surrounding ws":  " " + signerA + "\n",
		"blank line":      signerA + "\n\n" + signerB + "\n",
		"short":           "0x1234\n",
		"comment":         "# operators\n" + signerA + "\n",
		"duplicate":       signerA + "\n" + signerA + "\n",
		"no 0x prefix":    signerA[2:] + "\n",
		"windows newline": signerA + "\r\n",
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := ParseAnchor([]byte(data)); err == nil {
				t.Fatalf("accepted %q as %v", data, got)
			}
		})
	}
}

func TestNormalizeSigners_lowercasesAndTrims(t *testing.T) {
	// An EIP-55 test vector, all-uppercase hex, and all-lowercase hex.
	const checksummed = "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed"
	got, err := NormalizeSigners([]string{" " + checksummed + " ", "0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", signerB})
	if err != nil {
		t.Fatalf("NormalizeSigners: %v", err)
	}
	if !slices.Equal(got, []string{strings.ToLower(checksummed), "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", signerB}) {
		t.Fatalf("got %v", got)
	}
}

func TestNormalizeSigners_refusesEmptyInvalidAndDuplicate(t *testing.T) {
	for name, in := range map[string][]string{
		"nil":       nil,
		"empty":     {},
		"not hex":   {"0xzz11111111111111111111111111111111111111"},
		"duplicate": {signerA, " " + signerA},
		// The EIP-55 vector above with one letter's case flipped: a typo.
		"bad checksum": {"0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAeD"},
		"0X prefix":    {"0X5aaeb6053f3e94c9b9a09f33669435e7ef1beaed"},
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := NormalizeSigners(in); err == nil {
				t.Fatalf("accepted %v as %v", in, got)
			}
		})
	}
}

func TestReadAnchor_missingAnchorIsErrNoAnchor(t *testing.T) {
	dir := t.TempDir()
	anchorSeams(t, dir)
	_, err := ReadAnchor(filepath.Join(dir, "archive-signers"))
	if !errors.Is(err, ErrNoAnchor) {
		t.Fatalf("got %v, want ErrNoAnchor", err)
	}
	if !strings.Contains(err.Error(), "--trust-signers") {
		t.Errorf("the error does not say how a pre-signing node gets an anchor: %v", err)
	}
}

func TestWriteThenReadAnchor_roundTripsAndIsRootOwned0644(t *testing.T) {
	dir := t.TempDir()
	chowned := anchorSeams(t, dir)
	path := filepath.Join(dir, "etc-orama", "archive-signers")

	if err := WriteAnchor(path, []string{" " + signerA, signerB}); err != nil {
		t.Fatalf("WriteAnchor: %v", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Errorf("mode %#o, want 0644", info.Mode().Perm())
	}
	if !slices.Equal(*chowned, []string{path}) {
		t.Errorf("chown to root was asked for %v, want exactly %s", *chowned, path)
	}
	data, _ := os.ReadFile(path)
	if string(data) != signerA+"\n"+signerB+"\n" {
		t.Errorf("anchor contents %q", data)
	}
	got, err := ReadAnchor(path)
	if err != nil || !slices.Equal(got, []string{signerA, signerB}) {
		t.Fatalf("ReadAnchor = %v, %v", got, err)
	}
}

func TestWriteAnchor_refusesAnEmptyList(t *testing.T) {
	dir := t.TempDir()
	anchorSeams(t, dir)
	path := filepath.Join(dir, "archive-signers")
	if err := WriteAnchor(path, nil); err == nil {
		t.Fatal("wrote an anchor that trusts nobody")
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a refused write left a file behind: %v", err)
	}
}

func TestWriteAnchor_failedChownIsAnError(t *testing.T) {
	dir := t.TempDir()
	anchorSeams(t, dir)
	chownAnchor = func(rootfs.Root, string) error { return errors.New("operation not permitted") }
	if err := WriteAnchor(filepath.Join(dir, "archive-signers"), []string{signerA}); err == nil {
		t.Fatal("an anchor that could not be given to root was reported as written")
	}
}

func TestReadAnchor_refusesAnAnchorSomeoneElseOwns(t *testing.T) {
	dir := t.TempDir()
	anchorSeams(t, dir)
	path := filepath.Join(dir, "archive-signers")
	if err := os.WriteFile(path, []byte(signerA+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	anchorOwner.uid++
	if _, err := ReadAnchor(path); err == nil || !strings.Contains(err.Error(), "owned by") {
		t.Fatalf("trusted an anchor owned by someone else: %v", err)
	}
}

func TestReadAnchor_refusesAGroupOrWorldWritableAnchor(t *testing.T) {
	dir := t.TempDir()
	anchorSeams(t, dir)
	path := filepath.Join(dir, "archive-signers")
	if err := os.WriteFile(path, []byte(signerA+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []os.FileMode{0o664, 0o646} {
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadAnchor(path); err == nil || !strings.Contains(err.Error(), "writable") {
			t.Fatalf("trusted an anchor with mode %#o: %v", mode, err)
		}
	}
}

func TestReadAnchor_refusesASymlink(t *testing.T) {
	dir := t.TempDir()
	anchorSeams(t, dir)
	target := filepath.Join(dir, "elsewhere")
	if err := os.WriteFile(target, []byte(signerA+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "archive-signers")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadAnchor(path); err == nil {
		t.Fatalf("followed a symlinked anchor: %v", err)
	}
}

func TestReadAnchor_emptyAnchorIsAnError(t *testing.T) {
	dir := t.TempDir()
	anchorSeams(t, dir)
	path := filepath.Join(dir, "archive-signers")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ReadAnchor(path)
	if err == nil || errors.Is(err, ErrNoAnchor) {
		t.Fatalf("an empty anchor must be an error distinct from a missing one, got %v", err)
	}
}

func TestCreateAnchorIfMissing(t *testing.T) {
	dir := t.TempDir()
	anchorSeams(t, dir)
	path := filepath.Join(dir, "archive-signers")

	if err := CreateAnchorIfMissing(path, []string{signerA}); err != nil {
		t.Fatalf("creating a missing anchor: %v", err)
	}
	if err := CreateAnchorIfMissing(path, []string{signerA}); err != nil {
		t.Fatalf("asking for the list the anchor already holds must be a no-op: %v", err)
	}
	err := CreateAnchorIfMissing(path, []string{signerB})
	if err == nil || !strings.Contains(err.Error(), "already trusts") {
		t.Fatalf("replaced an existing anchor: %v", err)
	}
	if got, _ := ReadAnchor(path); !slices.Equal(got, []string{signerA}) {
		t.Fatalf("the refused call changed the anchor to %v", got)
	}
}

func TestReadAnchor_refusesADirectoryOthersCanWrite(t *testing.T) {
	dir := t.TempDir()
	anchorSeams(t, dir)
	path := filepath.Join(dir, "archive-signers")
	if err := os.WriteFile(path, []byte(signerA+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadAnchor(path); err == nil || !strings.Contains(err.Error(), "directory") {
		t.Fatalf("trusted an anchor in a directory anyone can write: %v", err)
	}
}

func TestWriteAnchor_refusesToReplaceAFileSomeoneElseOwns(t *testing.T) {
	dir := t.TempDir()
	anchorSeams(t, dir)
	path := filepath.Join(dir, "archive-signers")
	if err := os.WriteFile(path, []byte(signerA+"\n"), 0o666); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o666); err != nil {
		t.Fatal(err)
	}
	if err := WriteAnchor(path, []string{signerB}); err == nil || !strings.Contains(err.Error(), "remove it") {
		t.Fatalf("replaced an anchor others could write, keeping its writers: %v", err)
	}
}

func TestNormalizeSigners_refusesAListOverTheCap(t *testing.T) {
	var many []string
	for i := 0; i <= MaxSigners; i++ {
		many = append(many, fmt.Sprintf("0x%040x", i+1))
	}
	if _, err := NormalizeSigners(many); err == nil {
		t.Fatal("accepted more signers than the anchor is meant to hold")
	}
}

// The cap is applied to the input's length before any entry is parsed, so an
// enormous list costs nothing: every entry here is invalid, and the error is
// about the length.
func TestNormalizeSigners_overTheCapIsRefusedBeforeParsing(t *testing.T) {
	junk := make([]string, 100000)
	_, err := NormalizeSigners(junk)
	if err == nil || !strings.Contains(err.Error(), "over the limit") {
		t.Fatalf("got %v", err)
	}
}

func TestWriteAnchor_createsItsDirectory0755WhateverTheUmask(t *testing.T) {
	old := syscall.Umask(0o077)
	defer syscall.Umask(old)
	parent := t.TempDir()
	anchorSeams(t, parent)
	path := filepath.Join(parent, "etc-orama", "archive-signers")
	if err := WriteAnchor(path, []string{signerA}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != anchorDirMode {
		t.Fatalf("directory mode %#o, want %#o: the gateway could not reach the anchor", info.Mode().Perm(), anchorDirMode)
	}
}

// /etc/orama may exist already, made under a strict umask by something else
// (the Tor setup): the anchor write makes it traversable again.
func TestWriteAnchor_makesAnExistingDirectoryTraversable(t *testing.T) {
	parent := t.TempDir()
	anchorSeams(t, parent)
	dir := filepath.Join(parent, "etc-orama")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := WriteAnchor(filepath.Join(dir, "archive-signers"), []string{signerA}); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(dir); info.Mode().Perm() != anchorDirMode {
		t.Fatalf("directory mode %#o", info.Mode().Perm())
	}
}

// A directory others can write is evidence of tampering: the write refuses it
// rather than quietly tightening it.
func TestWriteAnchor_refusesADirectoryOthersCanWrite(t *testing.T) {
	parent := t.TempDir()
	anchorSeams(t, parent)
	dir := filepath.Join(parent, "etc-orama")
	if err := os.Mkdir(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := WriteAnchor(filepath.Join(dir, "archive-signers"), []string{signerA}); err == nil {
		t.Fatal("wrote the anchor into a world-writable directory")
	}
	if info, _ := os.Stat(dir); info.Mode().Perm() != 0o777 {
		t.Fatalf("the directory's mode was changed to %#o", info.Mode().Perm())
	}
}
