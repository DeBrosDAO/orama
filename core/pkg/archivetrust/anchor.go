// Package archivetrust decides whether a node may install a build archive.
//
// A node trusts the EVM addresses in its trust anchor, /etc/orama/archive-signers.
// An archive is installable only when its manifest.sig is an EIP-191
// personal_sign signature of SigningMessage(manifest.json) by one of them and
// every file it carries matches that manifest. `orama build` signs exactly
// that message, and a signed manifest may rotate the anchor (docs/SECURITY.md).
//
// It is a leaf package — rootfs and go-ethereum only — because the installer,
// the build, push, setup and the gateway's join handler all need it.
package archivetrust

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/rootfs"
	"github.com/ethereum/go-ethereum/common"
)

// AnchorPath is the node's archive trust anchor: the EVM addresses whose
// signature on a build archive's manifest this node accepts, one lowercase 0x
// address per line.
//
// It is root:root 0644 in /etc/orama, outside /opt/orama, so neither the orama
// user nor anything an archive carries can change who is trusted. Only root
// install and upgrade code writes it: a genesis install seeds it from
// --operator-wallet, a join copies the minting node's list, and an archive
// signed by a trusted signer can rotate it.
const AnchorPath = "/etc/orama/archive-signers"

const (
	// anchorPerm lets the gateway, which runs as orama, read the list it
	// hands a joining node; only root can write it.
	anchorPerm fs.FileMode = 0o644
	// anchorDirMode is the anchor's directory when WriteAnchor creates it:
	// services may traverse it, only root may write it.
	anchorDirMode fs.FileMode = 0o755
	// MaxSigners bounds a signer list; the anchor file stays far below
	// anchorFileLimit. The join handler checks a request's list against it
	// before anything else is done with it.
	MaxSigners = 32
	// anchorFileLimit bounds a read of the anchor: a few addresses.
	anchorFileLimit = 64 << 10
	// writableByGroupOrOthers are the mode bits that would let someone other
	// than root rewrite a file or add one to a directory.
	writableByGroupOrOthers fs.FileMode = 0o022
	// rootID is root's uid and gid.
	rootID = 0
)

// ErrNoAnchor is wrapped by the error for a node that has no trust anchor at
// all, as opposed to one that is unreadable or malformed.
var ErrNoAnchor = errors.New("no archive trust anchor")

// signerPattern is one anchor line: a lowercase 0x EVM address.
var signerPattern = regexp.MustCompile(`^0x[0-9a-f]{40}$`)

// anyCaseSignerPattern is an address as an operator may type it.
var anyCaseSignerPattern = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)

// anchorOwner is who must own the anchor and its directory. A test, which
// cannot chown to root, sets it to itself.
var anchorOwner = fileOwnerIDs{uid: rootID, gid: rootID}

// chownAnchor gives a freshly written anchor file to anchorOwner. A test
// replaces it to record the call instead.
var chownAnchor = func(root rootfs.Root, path string) error {
	return root.Chown(path, rootID, rootID)
}

// fileOwnerIDs is a file's numeric owner.
type fileOwnerIDs struct {
	uid, gid uint32
}

// NormalizeSigners turns operator input into the anchor's form. An address in
// one case is accepted as is; one in mixed case must carry a valid EIP-55
// checksum, which catches a mistyped character. Anything that is not a
// non-empty list of distinct addresses is refused.
func NormalizeSigners(addrs []string) ([]string, error) {
	// Before any per-entry work: the duplicate check is quadratic, and the
	// join handler normalizes a list from the request body.
	if len(addrs) > MaxSigners {
		return nil, fmt.Errorf("the signer list names %d addresses, over the limit of %d", len(addrs), MaxSigners)
	}
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		addr := strings.TrimSpace(a)
		if !anyCaseSignerPattern.MatchString(addr) {
			return nil, fmt.Errorf("%q is not an EVM address: expected 0x followed by 40 hex characters", a)
		}
		hexPart := addr[2:]
		mixed := hexPart != strings.ToLower(hexPart) && hexPart != strings.ToUpper(hexPart)
		if mixed && common.HexToAddress(addr).Hex() != addr {
			return nil, fmt.Errorf("%s fails its EIP-55 checksum: a character is mistyped (copy the address "+
				"again, or give it in one case to skip the check)", addr)
		}
		addr = strings.ToLower(addr)
		if slices.Contains(out, addr) {
			return nil, fmt.Errorf("%s is listed twice", addr)
		}
		out = append(out, addr)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("the signer list is empty: a node that trusts nobody can install nothing")
	}
	return out, nil
}

// ParseAnchor reads the anchor's contents: one lowercase 0x address per line,
// newline-terminated, nothing else.
func ParseAnchor(data []byte) ([]string, error) {
	text := strings.TrimSuffix(string(data), "\n")
	if text == "" {
		return nil, fmt.Errorf("it is empty: a node that trusts nobody can install nothing")
	}
	var out []string
	for i, line := range strings.Split(text, "\n") {
		if !signerPattern.MatchString(line) {
			return nil, fmt.Errorf("line %d (%q) is not a lowercase 0x EVM address", i+1, line)
		}
		if slices.Contains(out, line) {
			return nil, fmt.Errorf("line %d lists %s twice", i+1, line)
		}
		out = append(out, line)
	}
	return out, nil
}

// formatAnchor renders the anchor's contents.
func formatAnchor(signers []string) []byte {
	return []byte(strings.Join(signers, "\n") + "\n")
}

// errMissingAnchor is the error for path not existing.
func errMissingAnchor(path string) error {
	return fmt.Errorf("%w: %s does not exist, so this node cannot verify any build archive. "+
		"A genesis install creates it from --operator-wallet and a joining node from the cluster it "+
		"joins; a node installed before archives were signed gets it once from "+
		"`orama push --trust-signers <address>` (docs/SECURITY.md)", ErrNoAnchor, path)
}

// ReadAnchor reads the anchor at path. Its directory must be a real directory
// only root can write, and the file a regular file owned by root and writable
// by root alone; the checks are made on the descriptor that is read, so the
// file cannot be swapped between check and read. A missing file is an error
// wrapping ErrNoAnchor.
func ReadAnchor(path string) ([]string, error) {
	data, err := readRootOwned(path, anchorFileLimit, "the archive trust anchor")
	if errors.Is(err, fs.ErrNotExist) {
		return nil, errMissingAnchor(path)
	}
	if err != nil {
		return nil, err
	}
	signers, err := ParseAnchor(data)
	if err != nil {
		return nil, fmt.Errorf("the archive trust anchor %s is invalid: %w", path, err)
	}
	return signers, nil
}

// readRootOwned reads a small file only root can have written. A missing file
// or directory is an error matching fs.ErrNotExist.
func readRootOwned(path string, limit int64, what string) ([]byte, error) {
	if err := checkAnchorDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	f, info, err := openNoFollow(path)
	if err != nil {
		return nil, fmt.Errorf("open %s %s: %w", what, path, err)
	}
	defer f.Close()
	if err := checkOwnedByRootOnly(path, info, what); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read %s %s: %w", what, path, err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s %s is over %d bytes", what, path, limit)
	}
	return data, nil
}

// checkAnchorDir refuses a directory for the anchor that is a symlink, or that
// someone other than root could add files to or rename files in.
func checkAnchorDir(dir string) error {
	info, err := lstat(dir)
	if err != nil {
		return fmt.Errorf("stat %s: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s, the directory of the archive trust anchor, is not a directory (%s)", dir, info.Mode().Type())
	}
	return checkOwnedByRootOnly(dir, info, "the directory of the archive trust anchor")
}

// checkOwnedByRootOnly refuses what anyone but root could have written.
func checkOwnedByRootOnly(path string, info fs.FileInfo, what string) error {
	if !info.Mode().IsRegular() && !info.IsDir() {
		return fmt.Errorf("%s %s is not a regular file (%s); refusing to trust it", what, path, info.Mode().Type())
	}
	owner, err := ownerOf(info)
	if err != nil {
		return fmt.Errorf("%s %s: %w", what, path, err)
	}
	if owner != anchorOwner {
		return fmt.Errorf("%s %s is owned by %d:%d, not %d:%d; whoever owns it decides which builds this "+
			"node runs, so it is refused (chown root:root %s after checking it)",
			what, path, owner.uid, owner.gid, anchorOwner.uid, anchorOwner.gid, path)
	}
	if info.Mode().Perm()&writableByGroupOrOthers != 0 {
		return fmt.Errorf("%s %s is writable by group or others (mode %#o); refusing to trust it "+
			"(chmod go-w %s after checking it)", what, path, info.Mode().Perm(), path)
	}
	return nil
}

// WriteAnchor replaces the anchor at path with signers, atomically and
// without following a symlink, and leaves it root:root 0644.
func WriteAnchor(path string, signers []string) error {
	normalized, err := NormalizeSigners(signers)
	if err != nil {
		return fmt.Errorf("refusing to write the archive trust anchor: %w", err)
	}
	return writeRootOwned(path, formatAnchor(normalized), "the archive trust anchor")
}

// writeRootOwned replaces the file at path with data, atomically and without
// following a symlink, and leaves it root:root 0644. An existing file someone
// other than root owns is refused rather than replaced: its owner could hold
// it open and write to the new contents after the fact.
func writeRootOwned(path string, data []byte, what string) error {
	root := rootfs.At(filepath.Dir(path))
	if err := ensureAnchorDir(root.Dir()); err != nil {
		return err
	}
	if err := checkAnchorDir(root.Dir()); err != nil {
		return err
	}
	if info, err := lstat(path); err == nil {
		if err := checkOwnedByRootOnly(path, info, "the existing "+strings.TrimPrefix(what, "the ")); err != nil {
			return fmt.Errorf("%w; remove it and run this again", err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("stat %s %s: %w", what, path, err)
	}
	if err := root.WriteFile(path, data, anchorPerm); err != nil {
		return fmt.Errorf("write %s: %w", what, err)
	}
	if err := chownAnchor(root, path); err != nil {
		return fmt.Errorf("give %s %s to root: %w", what, path, err)
	}
	return nil
}

// CreateAnchorIfMissing writes the anchor for a node that has none — a node
// installed before archives were signed. It never changes an existing anchor:
// asking for the list it already holds is a no-op, and asking for a different
// one is refused, because changing who is trusted is what a signed rotation
// (`orama build --signers`) is for.
func CreateAnchorIfMissing(path string, signers []string) error {
	want, err := NormalizeSigners(signers)
	if err != nil {
		return err
	}
	have, err := ReadAnchor(path)
	if errors.Is(err, ErrNoAnchor) {
		// A new anchor has no rotation history: a mark an earlier cluster left
		// beside a removed anchor would refuse this one's rotations.
		if err := RemoveRotationMark(path); err != nil {
			return err
		}
		return WriteAnchor(path, want)
	}
	if err != nil {
		return err
	}
	if SameSigners(have, want) {
		return nil
	}
	return fmt.Errorf("%s already trusts %s; --trust-signers only creates a missing anchor. "+
		"To change who signs builds, install an archive built with `orama build --signers` and "+
		"signed by a signer this node already trusts", path, strings.Join(have, ", "))
}

// SameSigners reports whether a and b hold the same addresses in any order.
func SameSigners(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for _, s := range a {
		if !slices.Contains(b, s) {
			return false
		}
	}
	return true
}

// ensureAnchorDir makes the anchor's directory exist and be traversable: a
// strict umask — here, or wherever else created /etc/orama first (the Tor
// setup writes /etc/orama/tor) — must not leave it closed to the gateway,
// which reads the anchor as the orama user. It only ever adds the read and
// search bits, and only to a directory root owns that nobody else can write;
// any other directory is left as it is for checkAnchorDir to refuse, so
// evidence of tampering is not repaired away.
func ensureAnchorDir(dir string) error {
	info, err := lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		if err := os.Mkdir(dir, anchorDirMode); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
		if err := os.Chmod(dir, anchorDirMode); err != nil {
			return fmt.Errorf("chmod %s: %w", dir, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat %s: %w", dir, err)
	}
	if !info.IsDir() || checkOwnedByRootOnly(dir, info, "") != nil || info.Mode().Perm() == anchorDirMode {
		return nil
	}
	if err := os.Chmod(dir, info.Mode()&(fs.ModePerm|fs.ModeSetgid|fs.ModeSticky)|anchorDirMode); err != nil {
		return fmt.Errorf("chmod %s: %w", dir, err)
	}
	return nil
}
