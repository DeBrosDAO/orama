package archivetrust

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/rootfs"
)

// testArch is what test archives are built for; testNow is the node's clock.
const testArch = "amd64"

var testNow = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

// trustingAnchor writes an anchor trusting signers in a fresh directory, with
// the clock at testNow.
func trustingAnchor(t *testing.T, signers ...string) (string, *[]string) {
	t.Helper()
	prevNow := now
	t.Cleanup(func() { now = prevNow })
	now = func() time.Time { return testNow }
	dir := t.TempDir()
	chowned := anchorSeams(t, dir)
	anchor := filepath.Join(dir, "archive-signers")
	if err := WriteAnchor(anchor, signers); err != nil {
		t.Fatal(err)
	}
	return anchor, chowned
}

func TestVerifyAndRotate_rotatesWhenSignedByACurrentSigner(t *testing.T) {
	s := newTestSigner(t)
	anchor, chowned := trustingAnchor(t, s.addr)
	dir := writeTestArchive(t, s, testFiles, []string{s.addr, signerB})

	_, rotated, err := VerifyAndRotate(anchor, dir, testArch)
	if err != nil || !rotated {
		t.Fatalf("rotated=%v err=%v", rotated, err)
	}
	if got, _ := ReadAnchor(anchor); !slices.Equal(got, []string{s.addr, signerB}) {
		t.Fatalf("anchor is %v after the rotation", got)
	}
	if at, err := ReadRotationMark(anchor); err != nil || at.Format(time.RFC3339) != testBuildDate {
		t.Fatalf("rotation mark = %v, %v; want the build date", at, err)
	}
	if len(*chowned) != 3 {
		t.Errorf("the rotated anchor and its mark were not both given to root: chown calls %v", *chowned)
	}
	if (*chowned)[1] != RotationMarkPath(anchor) {
		t.Errorf("the mark must be written before the anchor: %v", *chowned)
	}
}

// Re-running an interrupted upgrade, or a join through a node that already
// rotated, verifies the same archive against the rotated anchor.
func TestVerifyAndRotate_theSameArchiveVerifiesAgainAfterItsRotation(t *testing.T) {
	s := newTestSigner(t)
	anchor, _ := trustingAnchor(t, s.addr)
	dir := writeTestArchive(t, s, testFiles, []string{s.addr, signerB})

	if _, rotated, err := VerifyAndRotate(anchor, dir, testArch); err != nil || !rotated {
		t.Fatalf("first run: rotated=%v err=%v", rotated, err)
	}
	if _, rotated, err := VerifyAndRotate(anchor, dir, testArch); err != nil || rotated {
		t.Fatalf("second run on the same archive: rotated=%v err=%v", rotated, err)
	}
}

// Retiring a key takes two builds: the old key adds the new one, then the new
// key drops the old one. The first build cannot be replayed afterwards.
func TestVerifyAndRotate_twoBuildRetirementAndNoReplay(t *testing.T) {
	oldKey, newKey := newTestSigner(t), newTestSigner(t)
	anchor, _ := trustingAnchor(t, oldKey.addr)

	adds := writeTestArchiveAt(t, oldKey, testFiles, []string{oldKey.addr, newKey.addr}, "2026-09-01T00:00:00Z")
	if _, _, err := VerifyAndRotate(anchor, adds, testArch); err != nil {
		t.Fatalf("old key adding the new one: %v", err)
	}
	drops := writeTestArchiveAt(t, newKey, testFiles, []string{newKey.addr}, "2026-09-02T00:00:00Z")
	if _, _, err := VerifyAndRotate(anchor, drops, testArch); err != nil {
		t.Fatalf("new key dropping the old one: %v", err)
	}
	if got, _ := ReadAnchor(anchor); !slices.Equal(got, []string{newKey.addr}) {
		t.Fatalf("anchor = %v", got)
	}
	if _, _, err := VerifyAndRotate(anchor, adds, testArch); err == nil {
		t.Fatal("the retired key's build was accepted again")
	}
}

// A key still trusted cannot replay one of its own older builds whose list
// named a key retired since.
func TestVerifyAndRotate_anOlderRotationIsNotReplayed(t *testing.T) {
	s := newTestSigner(t)
	anchor, _ := trustingAnchor(t, s.addr)

	withB := writeTestArchiveAt(t, s, testFiles, []string{s.addr, signerB}, "2026-09-01T00:00:00Z")
	withoutB := writeTestArchiveAt(t, s, testFiles, []string{s.addr}, "2026-09-02T00:00:00Z")
	for _, dir := range []string{withB, withoutB} {
		if _, _, err := VerifyAndRotate(anchor, dir, testArch); err != nil {
			t.Fatal(err)
		}
	}
	_, _, err := VerifyAndRotate(anchor, withB, testArch)
	if err == nil || !strings.Contains(err.Error(), "replayed") {
		t.Fatalf("an older rotation put a retired key back: %v", err)
	}
	if got, _ := ReadAnchor(anchor); !slices.Equal(got, []string{s.addr}) {
		t.Fatalf("anchor = %v after a refused replay", got)
	}
}

func TestVerify_checksARotationButChangesNothing(t *testing.T) {
	s := newTestSigner(t)
	anchor, chowned := trustingAnchor(t, s.addr)
	if _, err := Verify(anchor, writeTestArchive(t, s, testFiles, []string{s.addr, signerB}), testArch); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got, _ := ReadAnchor(anchor); !slices.Equal(got, []string{s.addr}) || len(*chowned) != 1 {
		t.Fatalf("Verify changed the anchor to %v", got)
	}
	if _, err := Verify(anchor, writeTestArchiveAt(t, s, testFiles, []string{s.addr, signerB}, "yesterday"), testArch); err == nil {
		t.Fatal("accepted a rotation without a usable build date")
	}
}

func TestVerifyAndRotate_refusesARotationFromAnUntrustedSigner(t *testing.T) {
	anchor, _ := trustingAnchor(t, signerA)
	attacker := newTestSigner(t)
	if _, _, err := VerifyAndRotate(anchor, writeTestArchive(t, attacker, testFiles, []string{attacker.addr}), testArch); err == nil {
		t.Fatal("an untrusted signer rotated the anchor")
	}
	if got, _ := ReadAnchor(anchor); !slices.Equal(got, []string{signerA}) {
		t.Fatalf("a refused rotation changed the anchor to %v", got)
	}
}

func TestVerifyAndRotate_leavesTheAnchorWithoutARotation(t *testing.T) {
	s := newTestSigner(t)
	anchor, _ := trustingAnchor(t, s.addr)
	for _, rotation := range [][]string{nil, {s.addr}} {
		if _, rotated, err := VerifyAndRotate(anchor, writeTestArchive(t, s, testFiles, rotation), testArch); err != nil || rotated {
			t.Fatalf("rotation %v: rotated=%v err=%v", rotation, rotated, err)
		}
	}
	if got, _ := ReadAnchor(anchor); !slices.Equal(got, []string{s.addr}) {
		t.Fatalf("anchor = %v", got)
	}
}

// A build naming the list the anchor already holds advances the mark, which
// also repairs a mark write that failed after an earlier rotation.
func TestVerifyAndRotate_theSameListWithANewerBuildAdvancesTheMark(t *testing.T) {
	s := newTestSigner(t)
	anchor, _ := trustingAnchor(t, s.addr)
	if _, _, err := VerifyAndRotate(anchor, writeTestArchive(t, s, testFiles, []string{s.addr}), testArch); err != nil {
		t.Fatal(err)
	}
	if at, _ := ReadRotationMark(anchor); at.Format(time.RFC3339) != testBuildDate {
		t.Fatalf("mark = %v, want the build date", at)
	}
}

func TestVerifyAndRotate_aFailedMarkWriteChangesNothingAndARetryCompletes(t *testing.T) {
	s := newTestSigner(t)
	anchor, _ := trustingAnchor(t, s.addr)
	dir := writeTestArchive(t, s, testFiles, []string{s.addr, signerB})
	fail := true
	prev := chownAnchor
	chownAnchor = func(root rootfs.Root, path string) error {
		if fail && path == RotationMarkPath(anchor) {
			fail = false
			return errors.New("disk full")
		}
		return prev(root, path)
	}
	if _, _, err := VerifyAndRotate(anchor, dir, testArch); err == nil {
		t.Fatal("a failed mark write was reported as a rotation")
	}
	if got, _ := ReadAnchor(anchor); !slices.Equal(got, []string{s.addr}) {
		t.Fatalf("the anchor changed although its mark was not recorded: %v", got)
	}
	if _, rotated, err := VerifyAndRotate(anchor, dir, testArch); err != nil || !rotated {
		t.Fatalf("retry: rotated=%v err=%v", rotated, err)
	}
}

// The mark is written before the anchor: an interruption between the two is
// finished by running the same archive again (equal date = resume).
func TestVerifyAndRotate_resumesARotationInterruptedAfterTheMark(t *testing.T) {
	s := newTestSigner(t)
	anchor, _ := trustingAnchor(t, s.addr)
	dir := writeTestArchive(t, s, testFiles, []string{s.addr, signerB})
	prev := writeAnchor
	t.Cleanup(func() { writeAnchor = prev })
	writeAnchor = func(string, []string) error { return errors.New("killed") }
	if _, _, err := VerifyAndRotate(anchor, dir, testArch); err == nil {
		t.Fatal("an interrupted rotation was reported as done")
	}
	writeAnchor = prev
	if _, rotated, err := VerifyAndRotate(anchor, dir, testArch); err != nil || !rotated {
		t.Fatalf("resume: rotated=%v err=%v", rotated, err)
	}
	if got, _ := ReadAnchor(anchor); !slices.Equal(got, []string{s.addr, signerB}) {
		t.Fatalf("anchor = %v", got)
	}
}

func TestVerifyAndRotate_refusesARotationDatedFarInTheFuture(t *testing.T) {
	s := newTestSigner(t)
	anchor, _ := trustingAnchor(t, s.addr)
	future := testNow.Add(MaxRotationClockSkew + time.Minute).Format(time.RFC3339)
	_, _, err := VerifyAndRotate(anchor, writeTestArchiveAt(t, s, testFiles, []string{s.addr, signerB}, future), testArch)
	if err == nil || !strings.Contains(err.Error(), "clock") {
		t.Fatalf("a future-dated rotation would block every later one: %v", err)
	}
}

func TestVerify_refusesAnotherArchitectureBeforeRotating(t *testing.T) {
	s := newTestSigner(t)
	anchor, _ := trustingAnchor(t, s.addr)
	dir := writeTestArchive(t, s, testFiles, []string{s.addr, signerB})
	if _, _, err := VerifyAndRotate(anchor, dir, "arm64"); err == nil {
		t.Fatal("an amd64 archive verified on an arm64 node")
	}
	if got, _ := ReadAnchor(anchor); !slices.Equal(got, []string{s.addr}) {
		t.Fatalf("a refused archive rotated the anchor to %v", got)
	}
}

func TestRemoveRotationMark(t *testing.T) {
	anchor, _ := trustingAnchor(t, signerA)
	if err := RemoveRotationMark(anchor); err != nil {
		t.Fatalf("removing a missing mark: %v", err)
	}
	if err := WriteRotationMark(anchor, testNow); err != nil {
		t.Fatal(err)
	}
	if err := RemoveRotationMark(anchor); err != nil {
		t.Fatal(err)
	}
	if at, err := ReadRotationMark(anchor); err != nil || !at.IsZero() {
		t.Fatalf("mark still there: %v, %v", at, err)
	}
}

func TestVerifyAndRotate_refusesAnInvalidSignerList(t *testing.T) {
	s := newTestSigner(t)
	anchor, _ := trustingAnchor(t, s.addr)
	for _, rotation := range [][]string{{"0xnot-an-address"}, {s.addr, s.addr}} {
		if _, _, err := VerifyAndRotate(anchor, writeTestArchive(t, s, testFiles, rotation), testArch); err == nil {
			t.Fatalf("accepted the rotation %q", rotation)
		}
	}
	if got, _ := ReadAnchor(anchor); !slices.Equal(got, []string{s.addr}) {
		t.Fatalf("an invalid rotation changed the anchor to %v", got)
	}
}

func TestVerifyAndRotate_missingAnchorIsAHardFailure(t *testing.T) {
	s := newTestSigner(t)
	dir := t.TempDir()
	anchorSeams(t, dir)
	if _, _, err := VerifyAndRotate(filepath.Join(dir, "archive-signers"), writeTestArchive(t, s, testFiles, nil), testArch); err == nil {
		t.Fatal("verified an archive on a node with no trust anchor")
	}
}

func TestReadRotationMark_noMarkIsTheZeroTime(t *testing.T) {
	anchor, _ := trustingAnchor(t, signerA)
	at, err := ReadRotationMark(anchor)
	if err != nil || !at.IsZero() {
		t.Fatalf("got %v, %v", at, err)
	}
	if err := os.WriteFile(RotationMarkPath(anchor), []byte("garbage\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRotationMark(anchor); err == nil {
		t.Fatal("accepted an unparseable rotation mark")
	}
}

// A build with the list the anchor already holds, dated far ahead, would carry
// the mark ahead of every clock and refuse every rotation after it.
func TestVerifyAndRotate_theSameListDatedFarInTheFutureDoesNotAdvanceTheMark(t *testing.T) {
	s := newTestSigner(t)
	anchor, _ := trustingAnchor(t, s.addr)
	future := testNow.Add(24 * time.Hour).Format(time.RFC3339)
	if _, _, err := VerifyAndRotate(anchor, writeTestArchiveAt(t, s, testFiles, []string{s.addr}, future), testArch); err == nil {
		t.Fatal("a future-dated build advanced the mark")
	}
	if at, _ := ReadRotationMark(anchor); !at.IsZero() {
		t.Fatalf("mark = %v", at)
	}
}

// An anchor removed by hand leaves its mark behind; a new anchor must not
// inherit it.
func TestCreateAnchorIfMissing_dropsAStaleRotationMark(t *testing.T) {
	dir := t.TempDir()
	anchorSeams(t, dir)
	anchor := filepath.Join(dir, "archive-signers")
	if err := WriteRotationMark(anchor, testNow); err != nil {
		t.Fatal(err)
	}
	if err := CreateAnchorIfMissing(anchor, []string{signerA}); err != nil {
		t.Fatal(err)
	}
	if at, err := ReadRotationMark(anchor); err != nil || !at.IsZero() {
		t.Fatalf("a new anchor inherited the mark %v (%v)", at, err)
	}
}
