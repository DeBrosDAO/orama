package archivetrust

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"time"

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
// list, applies it. A different list becomes the anchor, recorded with the
// build date first so an interruption between the two writes is finished by
// running this again on the same archive. The same list with a newer build
// date only advances the mark. It reports whether the anchor changed. Because
// a rotation must name its own signer, verifying the same archive again
// afterwards succeeds.
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
	if !rot.changeAnchor {
		return v, false, nil
	}
	if err := writeAnchor(anchorPath, v.Signers); err != nil {
		return nil, false, fmt.Errorf("rotate the archive signers to %s: %w", strings.Join(v.Signers, ", "), err)
	}
	return v, true, nil
}

// rotation is what applying a verified archive's signer list takes.
type rotation struct {
	at           time.Time
	advanceMark  bool
	changeAnchor bool
}

// verifyWithRotation verifies the archive and works out what its signer list,
// if it names one, asks of the anchor and its mark (nil: nothing).
func verifyWithRotation(anchorPath, archiveDir, arch string) (*Verified, *rotation, error) {
	trusted, err := ReadAnchor(anchorPath)
	if err != nil {
		return nil, nil, err
	}
	v, err := VerifyTree(archiveDir, trusted)
	if err != nil {
		return nil, nil, err
	}
	if v.Manifest.Arch != arch {
		return nil, nil, fmt.Errorf("the archive is built for linux/%s and this node is linux/%s; build with --arch %s",
			v.Manifest.Arch, arch, arch)
	}
	if v.Signers == nil {
		return v, nil, nil
	}
	rot, err := planRotation(anchorPath, v, trusted)
	if err != nil {
		return nil, nil, err
	}
	return v, rot, nil
}

// planRotation decides what the verified signer list v carries asks of the
// anchor (trusted) and its mark.
func planRotation(anchorPath string, v *Verified, trusted []string) (*rotation, error) {
	at, err := time.Parse(time.RFC3339, v.Manifest.Date)
	if err != nil {
		return nil, fmt.Errorf("the signed manifest names signers but its build date %q is not an RFC 3339 time: %w",
			v.Manifest.Date, err)
	}
	last, err := ReadRotationMark(anchorPath)
	if err != nil {
		return nil, err
	}
	same := SameSigners(trusted, v.Signers)
	if at.After(last) && at.After(now().Add(MaxRotationClockSkew)) {
		// Whatever this build asks of the anchor, its date would become the
		// mark, and a mark ahead of every clock refuses every later rotation.
		return nil, fmt.Errorf("the archive names signers with a build dated %s, more than %s ahead of this "+
			"node's clock: the builder's clock or this node's clock is wrong; fix it and rebuild",
			at.UTC().Format(time.RFC3339), MaxRotationClockSkew)
	}
	switch {
	case same && !at.After(last):
		// Nothing to change: this build's list is the anchor, and the mark is
		// as new as this build or newer.
		return nil, nil
	case same:
		// The list is already the anchor, but the mark is older than this
		// build: advance it (this also repairs a mark write that failed).
		return &rotation{at: at, advanceMark: true}, nil
	case at.Before(last):
		return nil, fmt.Errorf("the archive rotates the signers with a build from %s, older than the rotation "+
			"this node last took (%s): an old build is being replayed — rotate with a new build",
			at.UTC().Format(time.RFC3339), last.UTC().Format(time.RFC3339))
	case at.Equal(last):
		// The mark already records this build: an interrupted rotation of
		// this same build, which only the anchor write has left to finish.
		return &rotation{at: at, changeAnchor: true}, nil
	default:
		return &rotation{at: at, advanceMark: true, changeAnchor: true}, nil
	}
}
