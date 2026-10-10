package archivetrust

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/releaseverify"
	"github.com/DeBrosOfficial/network/pkg/rootfs"
)

const (
	// rotationMarkSuffix names the file beside the anchor that records the
	// build date of the last rotation the anchor took.
	rotationMarkSuffix = ".rotated"
	// rotationMarkLimit bounds a read of it: one timestamp.
	rotationMarkLimit = 256
	// MaxRotationClockSkew is how far in this node's future a rotating build —
	// or a rotation mark a join hands over — may be dated. A build dated further ahead — a builder with a wrong clock
	// — would block every rotation after it until its date had passed.
	MaxRotationClockSkew = time.Hour
)

// now is the clock rotation dates are checked against, and writeAnchor how a
// rotation writes the anchor; a test replaces them.
var (
	now         = time.Now
	writeAnchor = WriteAnchor
)

// RotationMarkPath is the file that records the build date of the last
// rotation the anchor at anchorPath took. A rotation is accepted only from a
// build at least that new, so an old signed build — whose signer list may name
// a key retired since — cannot be replayed to put that key back.
func RotationMarkPath(anchorPath string) string {
	return anchorPath + rotationMarkSuffix
}

// ReadRotationMark returns the build date of the last rotation the anchor at
// anchorPath took; the zero time when it has taken none.
func ReadRotationMark(anchorPath string) (time.Time, error) {
	path := RotationMarkPath(anchorPath)
	data, err := readRootOwned(path, rotationMarkLimit, "the archive signer rotation mark")
	if errors.Is(err, fs.ErrNotExist) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	at, err := time.Parse(time.RFC3339, strings.TrimSpace(string(data)))
	if err != nil {
		return time.Time{}, fmt.Errorf("the archive signer rotation mark %s is invalid: %w", path, err)
	}
	return at, nil
}

// WriteRotationMark records at as the build date of the anchor's last
// rotation. A joining node copies the minting node's mark with its anchor.
func WriteRotationMark(anchorPath string, at time.Time) error {
	return writeRootOwned(RotationMarkPath(anchorPath), []byte(at.UTC().Format(time.RFC3339)+"\n"),
		"the archive signer rotation mark")
}

// RemoveRotationMark removes the mark beside the anchor at anchorPath: an
// anchor written from scratch (genesis, or a join into a cluster that never
// rotated) must not inherit an earlier cluster's. A missing mark is fine.
func RemoveRotationMark(anchorPath string) error {
	path := RotationMarkPath(anchorPath)
	err := rootfs.At(filepath.Dir(path)).Remove(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove the archive signer rotation mark: %w", err)
	}
	return nil
}

// Verify verifies the archive extracted in archiveDir against the anchor at
// anchorPath — its signature, every file, that it is built for arch, and
// whether a signer rotation it carries would be accepted — and changes
// nothing. An upgrade calls it before it stops any service.
func Verify(anchorPath, archiveDir, arch string) (*Verified, error) {
	v, _, err := verifyWithRotation(anchorPath, archiveDir, arch)
	return v, err
}

// VerifyAndRotate is Verify, then, when the verified manifest names a signer
// list or carries a release root, applies them. A different signer list
// becomes the anchor, recorded with the build date first so an interruption
// between the two writes is finished by running this again on the same
// archive. The same list with a newer build date only advances the mark. A
// release root other than the adopted one is adopted after both, under the
// same replay rule. It reports whether the anchor changed. Because a rotation
// must name its own signer, verifying the same archive again afterwards
// succeeds.
func VerifyAndRotate(anchorPath, archiveDir, arch string) (*Verified, bool, error) {
	v, rot, err := verifyWithRotation(anchorPath, archiveDir, arch)
	if err != nil || rot == nil {
		return v, false, err
	}
	if rot.advanceMark {
		if err := WriteRotationMark(anchorPath, rot.at); err != nil {
			return nil, false, fmt.Errorf("record the rotation to %s: %w", strings.Join(v.Signers, ", "), err)
		}
	}
	if rot.changeAnchor {
		if err := writeAnchor(anchorPath, v.Signers); err != nil {
			return nil, false, fmt.Errorf("rotate the archive signers to %s: %w", strings.Join(v.Signers, ", "), err)
		}
	}
	if rot.changeRoot {
		if _, err := releaseverify.AdoptRoot(ReleaseRootPath, v.ReleaseRoot, now()); err != nil {
			return nil, false, fmt.Errorf("adopt the release root the archive carries: %w", err)
		}
		v.AdoptedRoot = true
	}
	return v, rot.changeAnchor, nil
}

// rotation is what applying a verified archive's signer list and release root
// takes.
type rotation struct {
	at           time.Time
	advanceMark  bool
	changeAnchor bool
	changeRoot   bool
}

// verifyWithRotation verifies the archive and works out what its signer list
// and release root, if it names them, ask of the anchor, its mark and the
// adopted root (nil: nothing). An archive with no signature is verified
// through the release root when it was staged through it; such an archive asks
// for nothing.
func verifyWithRotation(anchorPath, archiveDir, arch string) (*Verified, *rotation, error) {
	trusted, err := ReadAnchor(anchorPath)
	if err != nil {
		return nil, nil, err
	}
	if v, err := verifyStagedRelease(archiveDir, arch); v != nil || err != nil {
		return v, nil, err
	}
	v, err := VerifyTree(archiveDir, trusted)
	if err != nil {
		return nil, nil, err
	}
	if v.Manifest.Arch != arch {
		return nil, nil, fmt.Errorf("the archive is built for linux/%s and this node is linux/%s; build with --arch %s",
			v.Manifest.Arch, arch, arch)
	}
	changeRoot, err := rootDiffers(v.ReleaseRoot)
	if err != nil {
		return nil, nil, err
	}
	if v.Signers == nil && !changeRoot {
		return v, nil, nil
	}
	rot, err := planRotation(anchorPath, v, trusted, changeRoot)
	if err != nil {
		return nil, nil, err
	}
	return v, rot, nil
}

// verifyStagedRelease verifies an archive that has no signature through the
// release root. It returns nil, nil for an archive that has one (or that was
// not staged that way), which the signature path then judges and refuses with
// its own message.
func verifyStagedRelease(archiveDir, arch string) (*Verified, error) {
	if _, err := lstat(filepath.Join(archiveDir, SignatureName)); err == nil {
		return nil, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("stat %s in the archive: %w", SignatureName, err)
	}
	v, err := VerifyReleaseTree(archiveDir, arch)
	if errors.Is(err, ErrNotStaged) {
		return nil, nil
	}
	return v, err
}

// rootDiffers reports whether root, the one a verified manifest carries, is
// other than the root this node has adopted. A manifest with no root differs
// from nothing.
func rootDiffers(root []byte) (bool, error) {
	if root == nil {
		return false, nil
	}
	current, err := releaseverify.ReadRoot(ReleaseRootPath)
	if errors.Is(err, releaseverify.ErrNoRoot) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return !bytes.Equal(current, root), nil
}

// planRotation decides what the verified signer list and release root v
// carries ask of the anchor (trusted), its mark and the adopted root.
func planRotation(anchorPath string, v *Verified, trusted []string, changeRoot bool) (*rotation, error) {
	at, err := time.Parse(time.RFC3339, v.Manifest.Date)
	if err != nil {
		return nil, fmt.Errorf("the signed manifest names signers or a release root but its build date %q is not an RFC 3339 time: %w",
			v.Manifest.Date, err)
	}
	last, err := ReadRotationMark(anchorPath)
	if err != nil {
		return nil, err
	}
	changeAnchor := v.Signers != nil && !SameSigners(trusted, v.Signers)
	if at.After(last) && at.After(now().Add(MaxRotationClockSkew)) {
		// Whatever this build asks of the anchor, its date would become the
		// mark, and a mark ahead of every clock refuses every later rotation.
		return nil, fmt.Errorf("the archive names signers or a release root with a build dated %s, more than %s ahead of this "+
			"node's clock: the builder's clock or this node's clock is wrong; fix it and rebuild",
			at.UTC().Format(time.RFC3339), MaxRotationClockSkew)
	}
	if (changeAnchor || changeRoot) && at.Before(last) {
		return nil, fmt.Errorf("the archive changes the signers or the release root with a build from %s, older than the rotation "+
			"this node last took (%s): an old build is being replayed — rotate with a new build",
			at.UTC().Format(time.RFC3339), last.UTC().Format(time.RFC3339))
	}
	if !changeAnchor && !changeRoot {
		if !at.After(last) {
			// This build's list is the anchor, and the mark is as new as
			// this build or newer.
			return nil, nil
		}
		// The list is already the anchor, but the mark is older than this
		// build: advance it (this also repairs a mark write that failed).
		return &rotation{at: at, advanceMark: true}, nil
	}
	// A mark equal to the build's date is an interrupted rotation of this same
	// build, which only the anchor and root writes have left to finish.
	return &rotation{at: at, advanceMark: at.After(last), changeAnchor: changeAnchor, changeRoot: changeRoot}, nil
}
