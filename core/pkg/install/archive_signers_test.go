package install

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/archivetrust"
)

const (
	signerA = "0x1111111111111111111111111111111111111111"
	signerB = "0x2222222222222222222222222222222222222222"
)

// fakeAnchor stands in for /etc/orama/archive-signers, which a test cannot
// make root-owned. The file handling itself is tested in pkg/archivetrust.
type fakeAnchor struct {
	signers []string // nil: no anchor
	writes  int
	mark    time.Time
	// archiveSigner is who signed the archive at archiveDir; verifyTree
	// passes only when it is trusted.
	archiveSigner string
}

func useFakeAnchor(t *testing.T, initial []string) *fakeAnchor {
	t.Helper()
	a := &fakeAnchor{signers: initial}
	a.archiveSigner = signerA
	prevRead, prevWrite, prevMark, prevRemove, prevTree := readArchiveSigners, writeArchiveSigners, writeRotationMark, removeRotationMark, verifyTree
	t.Cleanup(func() {
		readArchiveSigners, writeArchiveSigners, writeRotationMark, removeRotationMark, verifyTree = prevRead, prevWrite, prevMark, prevRemove, prevTree
	})
	writeRotationMark = func(_ string, at time.Time) error { a.mark = at; return nil }
	removeRotationMark = func(string) error { a.mark = time.Time{}; return nil }
	verifyTree = func(_ string, trusted []string) (*archivetrust.Verified, error) {
		if !slices.Contains(trusted, a.archiveSigner) {
			return nil, fmt.Errorf("signed by %s", a.archiveSigner)
		}
		return &archivetrust.Verified{Manifest: &PreBuiltManifest{Arch: nodeArch}, Signer: a.archiveSigner}, nil
	}
	readArchiveSigners = func(string) ([]string, error) {
		if a.signers == nil {
			return nil, fmt.Errorf("%w: missing", archivetrust.ErrNoAnchor)
		}
		return a.signers, nil
	}
	writeArchiveSigners = func(_ string, s []string) error {
		n, err := archivetrust.NormalizeSigners(s)
		if err != nil {
			return err
		}
		a.signers, a.writes = n, a.writes+1
		return nil
	}
	return a
}

func TestSeedGenesisArchiveSigners_createsTheAnchorFromTheOperatorWallet(t *testing.T) {
	a := useFakeAnchor(t, nil)
	ps := &ProductionSetup{OperatorWallet: "0x" + strings.ToUpper(signerA[2:])}

	if err := ps.SeedGenesisArchiveSigners(); err != nil {
		t.Fatalf("SeedGenesisArchiveSigners: %v", err)
	}
	if !slices.Equal(a.signers, []string{signerA}) {
		t.Fatalf("anchor = %v", a.signers)
	}
}

// A mistyped wallet must fail before it becomes the anchor: the archive does
// not verify against it.
func TestSeedGenesisArchiveSigners_writesNothingUnlessTheArchiveVerifies(t *testing.T) {
	a := useFakeAnchor(t, nil)
	if err := (&ProductionSetup{OperatorWallet: signerB}).SeedGenesisArchiveSigners(); err == nil {
		t.Fatal("seeded an anchor the archive does not verify against")
	}
	if a.writes != 0 {
		t.Fatalf("anchor written: %v", a.signers)
	}
}

func TestSeedGenesisArchiveSigners_dropsAnEarlierClustersRotationMark(t *testing.T) {
	a := useFakeAnchor(t, nil)
	a.mark = time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := (&ProductionSetup{OperatorWallet: signerA}).SeedGenesisArchiveSigners(); err != nil {
		t.Fatal(err)
	}
	if !a.mark.IsZero() {
		t.Fatalf("a stale rotation mark survived into the new cluster: %v", a.mark)
	}
}

func TestSeedGenesisArchiveSigners_withoutAnOperatorWalletIsRefused(t *testing.T) {
	a := useFakeAnchor(t, nil)
	err := (&ProductionSetup{}).SeedGenesisArchiveSigners()
	if err == nil || !strings.Contains(err.Error(), "--operator-wallet") {
		t.Fatalf("a genesis install without an operator wallet must fail and say why: %v", err)
	}
	if a.writes != 0 {
		t.Fatal("an anchor was written without a wallet")
	}
}

func TestSeedGenesisArchiveSigners_keepsAnAnchorThatTrustsOnlyTheWallet(t *testing.T) {
	a := useFakeAnchor(t, []string{signerA})
	if err := (&ProductionSetup{OperatorWallet: signerA}).SeedGenesisArchiveSigners(); err != nil {
		t.Fatalf("re-running the install: %v", err)
	}
	if a.writes != 0 {
		t.Fatalf("the existing anchor was rewritten to %v", a.signers)
	}
}

// Stale signers left from an earlier cluster must not survive into a new one
// just because the list also names the operator.
func TestSeedGenesisArchiveSigners_refusesAnAnchorWithOtherSignersToo(t *testing.T) {
	a := useFakeAnchor(t, []string{signerB, signerA})
	if err := (&ProductionSetup{OperatorWallet: signerA}).SeedGenesisArchiveSigners(); err == nil {
		t.Fatal("kept an anchor that also trusts a signer from elsewhere")
	}
	if a.writes != 0 {
		t.Fatal("the anchor changed")
	}
}

func TestSeedGenesisArchiveSigners_refusesToReplaceAnotherClustersAnchor(t *testing.T) {
	a := useFakeAnchor(t, []string{signerB})
	err := (&ProductionSetup{OperatorWallet: signerA}).SeedGenesisArchiveSigners()
	if err == nil || !strings.Contains(err.Error(), signerB) {
		t.Fatalf("replaced an anchor that does not trust the operator wallet: %v", err)
	}
	if a.writes != 0 {
		t.Fatalf("the refused seed changed the anchor to %v", a.signers)
	}
}

func TestSeedGenesisArchiveSigners_unreadableAnchorIsNotOverwritten(t *testing.T) {
	a := useFakeAnchor(t, nil)
	readArchiveSigners = func(string) ([]string, error) { return nil, errors.New("owned by 1000:1000") }
	if err := (&ProductionSetup{OperatorWallet: signerA}).SeedGenesisArchiveSigners(); err == nil {
		t.Fatal("an anchor that failed its ownership check was treated as missing")
	}
	if a.writes != 0 {
		t.Fatal("an unreadable anchor was overwritten")
	}
}

func TestTrustJoinedArchiveSigners_writesTheClustersList(t *testing.T) {
	a := useFakeAnchor(t, []string{signerB})
	if err := (&ProductionSetup{}).TrustJoinedArchiveSigners([]string{signerA}, "2026-09-20T10:00:00Z", nil); err != nil {
		t.Fatalf("TrustJoinedArchiveSigners: %v", err)
	}
	if !slices.Equal(a.signers, []string{signerA}) {
		t.Fatalf("anchor = %v, want the cluster's list", a.signers)
	}
	if a.mark.Format(time.RFC3339) != "2026-09-20T10:00:00Z" {
		t.Fatalf("rotation mark = %v, want the minting node's", a.mark)
	}
}

func TestTrustJoinedArchiveSigners_withoutARotationRemovesAStaleMark(t *testing.T) {
	a := useFakeAnchor(t, nil)
	a.mark = time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := (&ProductionSetup{}).TrustJoinedArchiveSigners([]string{signerA}, "", nil); err != nil {
		t.Fatalf("TrustJoinedArchiveSigners: %v", err)
	}
	if !a.mark.IsZero() {
		t.Fatalf("an earlier cluster's mark survived a join into one that never rotated: %v", a.mark)
	}
}

func TestTrustJoinedArchiveSigners_anUnparseableRotationTimeChangesNothing(t *testing.T) {
	a := useFakeAnchor(t, []string{signerB})
	if err := (&ProductionSetup{}).TrustJoinedArchiveSigners([]string{signerA}, "last tuesday", nil); err == nil {
		t.Fatal("accepted a rotation time that is not RFC 3339")
	}
	if a.writes != 0 {
		t.Fatalf("the anchor was written before the response was validated: %v", a.signers)
	}
}

// The list is served by the minting node's gateway, which runs as orama. The
// operator's expectation is what it must match.
func TestTrustJoinedArchiveSigners_refusesAListTheOperatorDidNotExpect(t *testing.T) {
	a := useFakeAnchor(t, nil)
	for _, got := range [][]string{{signerA, signerB}, {signerB}} {
		if err := (&ProductionSetup{}).TrustJoinedArchiveSigners(got, "", []string{signerA}); err == nil {
			t.Errorf("trusted %v when %v was expected", got, []string{signerA})
		}
	}
	if a.writes != 0 {
		t.Fatalf("anchor written: %v", a.signers)
	}
	if err := (&ProductionSetup{}).TrustJoinedArchiveSigners([]string{signerA}, "", []string{signerA}); err != nil {
		t.Fatalf("the expected list was refused: %v", err)
	}
}

func TestTrustJoinedArchiveSigners_anEmptyListIsRefused(t *testing.T) {
	a := useFakeAnchor(t, []string{signerB})
	err := (&ProductionSetup{}).TrustJoinedArchiveSigners(nil, "", nil)
	if err == nil || !strings.Contains(err.Error(), "sent no archive signers") {
		t.Fatalf("a join response without signers must fail the install: %v", err)
	}
	if a.writes != 0 {
		t.Fatal("the anchor changed")
	}
}

// useFakeVerify makes Phase 2b's verification return v, rotated and err, and
// the archive lock a no-op.
func useFakeVerify(t *testing.T, v *archivetrust.Verified, rotated bool, err error) {
	t.Helper()
	prev, prevLock := verifyAndRotate, lockArchive
	t.Cleanup(func() { verifyAndRotate, lockArchive = prev, prevLock })
	verifyAndRotate = func(string, string, string) (*archivetrust.Verified, bool, error) { return v, rotated, err }
	lockArchive = func(string) (func() error, error) { return func() error { return nil }, nil }
}

func TestVerifyPreBuiltArchive_refusesWhatDoesNotVerify(t *testing.T) {
	useFakeVerify(t, nil, false, errors.New("the archive is unsigned"))
	_, err := (&ProductionSetup{}).verifyPreBuiltArchive(&PreBuiltManifest{Version: "1"})
	if err == nil || !strings.Contains(err.Error(), "unsigned") {
		t.Fatalf("installed an archive that failed verification: %v", err)
	}
}

func TestVerifyPreBuiltArchive_refusesAManifestThatChangedAfterDetection(t *testing.T) {
	useFakeVerify(t, &archivetrust.Verified{Manifest: &PreBuiltManifest{Version: "2", Arch: nodeArch}, Signer: signerA}, false, nil)
	if _, err := (&ProductionSetup{}).verifyPreBuiltArchive(&PreBuiltManifest{Version: "1", Arch: nodeArch}); err == nil {
		t.Fatal("installed from a manifest other than the one that verified")
	}
}

func TestVerifyPreBuiltArchive_returnsTheVerifiedManifest(t *testing.T) {
	m := &PreBuiltManifest{Version: "1", Arch: nodeArch, Checksums: map[string]string{"orama": "ab"}}
	useFakeVerify(t, &archivetrust.Verified{Manifest: m, Signer: signerA}, true, nil)
	got, err := (&ProductionSetup{}).verifyPreBuiltArchive(&PreBuiltManifest{Version: "1", Arch: nodeArch, Checksums: map[string]string{"orama": "ab"}})
	if err != nil || got != m {
		t.Fatalf("got %v, %v", got, err)
	}
}

// Phase 2b used to compile /opt/orama/src, unverified, whenever the archive's
// manifest was missing or unreadable. Now there is nothing to fall back to.
func TestPhase2bInstallBinaries_withoutAnArchiveIsAHardFailure(t *testing.T) {
	if HasPreBuiltArchive() {
		t.Skipf("%s exists on this machine", OramaManifest)
	}
	err := (&ProductionSetup{}).Phase2bInstallBinaries()
	if err == nil || !strings.Contains(err.Error(), "no build archive") {
		t.Fatalf("Phase 2b without an archive must fail, got %v", err)
	}
}

// usePreflight points the join preflight at a fake archive signed by signer
// and built for arch.
func usePreflight(t *testing.T, signer, arch string) {
	t.Helper()
	dir := t.TempDir()
	prevDir, prevLock, prevTree, prevIntegrity := archiveDir, lockArchive, verifyTree, verifyIntegrity
	t.Cleanup(func() {
		archiveDir, lockArchive, verifyTree, verifyIntegrity = prevDir, prevLock, prevTree, prevIntegrity
	})
	archiveDir = dir
	lockArchive = func(string) (func() error, error) { return func() error { return nil }, nil }
	v := &archivetrust.Verified{Manifest: &PreBuiltManifest{Version: "1", Arch: arch}, Signer: signer}
	verifyIntegrity = func(string) (*archivetrust.Verified, error) { return v, nil }
	verifyTree = func(_ string, trusted []string) (*archivetrust.Verified, error) {
		if !slices.Contains(trusted, signer) {
			return nil, fmt.Errorf("signed by %s", signer)
		}
		return v, nil
	}
}

func TestPreflightJoinArchive(t *testing.T) {
	if HasPreBuiltArchive() {
		t.Skipf("%s exists on this machine", OramaManifest)
	}
	usePreflight(t, signerA, nodeArch)
	if err := (&ProductionSetup{}).PreflightJoinArchive(nil); err == nil {
		t.Fatal("no archive at all passed the preflight")
	}
}

func TestPreflightJoinArchive_checksArchitectureAndExpectedSigners(t *testing.T) {
	for name, tc := range map[string]struct {
		arch     string
		expected []string
		ok       bool
	}{
		"integrity only":       {nodeArch, nil, true},
		"expected signer":      {nodeArch, []string{signerA}, true},
		"unexpected signer":    {nodeArch, []string{signerB}, false},
		"another architecture": {"mips", nil, false},
	} {
		t.Run(name, func(t *testing.T) {
			usePreflight(t, signerA, tc.arch)
			err := (&ProductionSetup{}).preflightArchive(tc.expected)
			if (err == nil) != tc.ok {
				t.Fatalf("err = %v, want ok=%v", err, tc.ok)
			}
		})
	}
}

func TestTrustJoinedArchiveSigners_refusesAFutureRotationMark(t *testing.T) {
	a := useFakeAnchor(t, nil)
	future := time.Now().Add(archivetrust.MaxRotationClockSkew + time.Hour).UTC().Format(time.RFC3339)
	if err := (&ProductionSetup{}).TrustJoinedArchiveSigners([]string{signerA}, future, nil); err == nil {
		t.Fatal("accepted a rotation mark ahead of the clock")
	}
	if a.writes != 0 {
		t.Fatal("the anchor was written")
	}
}

func TestTrustJoinedArchiveSigners_aFailedMarkWriteLeavesNoAnchor(t *testing.T) {
	a := useFakeAnchor(t, nil)
	writeRotationMark = func(string, time.Time) error { return errors.New("disk full") }
	if err := (&ProductionSetup{}).TrustJoinedArchiveSigners([]string{signerA}, "2026-09-20T10:00:00Z", nil); err == nil {
		t.Fatal("a failed mark write was reported as success")
	}
	if a.writes != 0 {
		t.Fatalf("the anchor was written without its replay floor: %v", a.signers)
	}
}
