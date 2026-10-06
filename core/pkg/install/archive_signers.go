package install

import (
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/archivetrust"
)

// The archive trust anchor, its verification and the archive lock as install
// and upgrade reach them. A test replaces these: the real anchor must be owned
// by root.
var (
	archiveSignersPath  = archivetrust.AnchorPath
	readArchiveSigners  = archivetrust.ReadAnchor
	writeArchiveSigners = archivetrust.WriteAnchor
	writeRotationMark   = archivetrust.WriteRotationMark
	removeRotationMark  = archivetrust.RemoveRotationMark
	verifyTree          = archivetrust.VerifyTree
	verifyIntegrity     = archivetrust.VerifyIntegrity
	verifyArchive       = archivetrust.Verify
	verifyAndRotate     = archivetrust.VerifyAndRotate
	lockArchive         = archivetrust.LockArchiveDir
	// archiveDir is where the build archive is extracted.
	archiveDir = OramaBase
	// nodeArch is the architecture this node runs; an archive for another
	// one is refused.
	nodeArch = runtime.GOARCH
	// now is this node's clock.
	now = time.Now
)

// SeedGenesisArchiveSigners creates the trust anchor of a new cluster from the
// operator wallet. It must run before Phase 2b: the very first archive is
// verified against it, so that archive must already be signed by this wallet —
// which is checked before the anchor is written, so a mistyped wallet fails
// here and leaves nothing to undo.
//
// An existing anchor is kept only when it trusts exactly this wallet (a re-run
// of the same install). Any other is refused rather than overwritten or kept:
// it belongs to an earlier cluster or was rotated, and a new cluster starts
// trusting its operator and nobody else.
func (ps *ProductionSetup) SeedGenesisArchiveSigners() error {
	if strings.TrimSpace(ps.OperatorWallet) == "" {
		return fmt.Errorf("a genesis install needs --operator-wallet: that wallet becomes the only signer "+
			"this cluster accepts build archives from (%s), so the archive being installed must "+
			"already be signed by it — `orama node setup` passes your RootWallet address", archiveSignersPath)
	}
	wallet, err := archivetrust.NormalizeSigners([]string{ps.OperatorWallet})
	if err != nil {
		return fmt.Errorf("--operator-wallet: %w", err)
	}
	existing, err := readArchiveSigners(archiveSignersPath)
	switch {
	case errors.Is(err, archivetrust.ErrNoAnchor):
		return ps.createGenesisAnchor(wallet)
	case err != nil:
		return err
	case archivetrust.SameSigners(existing, wallet):
		ps.logf("  ✓ Archive trust anchor %s already trusts %s", archiveSignersPath, wallet[0])
		return nil
	default:
		return fmt.Errorf("%s already trusts %s, not only the operator wallet %s: it is left from an earlier "+
			"cluster or was rotated. Remove it and %s (or wipe the machine with `orama node wipe`) to start a "+
			"new cluster whose builds %s signs", archiveSignersPath, strings.Join(existing, ", "), wallet[0],
			archivetrust.RotationMarkPath(archiveSignersPath), wallet[0])
	}
}

// createGenesisAnchor writes a new cluster's anchor once the archive has
// verified against it, and drops any rotation mark an earlier cluster left.
func (ps *ProductionSetup) createGenesisAnchor(wallet []string) error {
	if _, err := verifyTree(archiveDir, wallet); err != nil {
		return fmt.Errorf("the build archive in %s does not verify against --operator-wallet %s, so no "+
			"trust anchor was written: %w", archiveDir, wallet[0], err)
	}
	if err := removeRotationMark(archiveSignersPath); err != nil {
		return err
	}
	if err := writeArchiveSigners(archiveSignersPath, wallet); err != nil {
		return err
	}
	ps.logf("  ✓ Archive trust anchor %s created: builds must be signed by %s", archiveSignersPath, wallet[0])
	return nil
}

// TrustJoinedArchiveSigners makes the list the minting node sent in the join
// response this node's anchor, with the minting node's rotation mark
// (rotatedAt, RFC 3339; empty when it never rotated). It must run before this
// node installs anything from its archive. The list arrives over the
// invite-authenticated, fingerprint-pinned join, but it is served by the
// minting node's gateway, which runs as the orama user: when the operator
// said which signers to expect (--expect-archive-signers), a list that is not
// exactly those is refused.
func (ps *ProductionSetup) TrustJoinedArchiveSigners(signers []string, rotatedAt string, expected []string) error {
	if len(signers) == 0 {
		return fmt.Errorf("the node that minted the invite sent no archive signers: it runs a release " +
			"from before archives were signed, or has no trust anchor of its own. Upgrade that node " +
			"(and give it an anchor with `orama push --trust-signers`), then mint a new invite")
	}
	normalized, err := archivetrust.NormalizeSigners(signers)
	if err != nil {
		return fmt.Errorf("the join response's archive signers: %w", err)
	}
	if len(expected) > 0 && !archivetrust.SameSigners(normalized, expected) {
		return fmt.Errorf("the cluster says it trusts %s, but --expect-archive-signers says %s; refusing to "+
			"trust a list the operator did not expect", strings.Join(normalized, ", "), strings.Join(expected, ", "))
	}
	var at time.Time
	if rotatedAt != "" {
		if at, err = time.Parse(time.RFC3339, rotatedAt); err != nil {
			return fmt.Errorf("the join response's archive signer rotation time %q is not RFC 3339: %w", rotatedAt, err)
		}
		if at.After(now().Add(archivetrust.MaxRotationClockSkew)) {
			return fmt.Errorf("the join response's archive signer rotation time %s is ahead of this node's clock "+
				"(the minting node's clock or this node's clock is wrong); it would refuse every later rotation", rotatedAt)
		}
	}
	// The mark first, as a rotation writes it: an anchor never stands without
	// the replay floor it came with.
	if at.IsZero() {
		err = removeRotationMark(archiveSignersPath)
	} else {
		err = writeRotationMark(archiveSignersPath, at)
	}
	if err != nil {
		return err
	}
	if err := writeArchiveSigners(archiveSignersPath, normalized); err != nil {
		return err
	}
	ps.logf("  ✓ Archive trust anchor %s: builds must be signed by %s", archiveSignersPath, strings.Join(normalized, ", "))
	return nil
}

// PreflightJoinArchive checks, before a join spends its invite, everything
// about the archive that can be checked without the cluster's signers: it is
// there, it is built for this node, and every file matches the manifest its
// signature covers — against expected when the operator named the signers,
// otherwise against whoever signed it. Only the signer's membership in the
// cluster's list is left for Phase 2b.
func (ps *ProductionSetup) PreflightJoinArchive(expected []string) error {
	if !HasPreBuiltArchive() {
		return fmt.Errorf("no build archive at %s (%s is missing): `orama node setup` puts one there", archiveDir, OramaManifest)
	}
	return ps.preflightArchive(expected)
}

// preflightArchive is PreflightJoinArchive once the archive is known to be
// there.
func (ps *ProductionSetup) preflightArchive(expected []string) error {
	unlock, err := lockArchive(archiveDir)
	if err != nil {
		return err
	}
	var verified *archivetrust.Verified
	if len(expected) > 0 {
		verified, err = verifyTree(archiveDir, expected)
	} else {
		verified, err = verifyIntegrity(archiveDir)
	}
	if err = errors.Join(err, unlock()); err != nil {
		return fmt.Errorf("the build archive in %s cannot be installed, so the join was not requested: %w", archiveDir, err)
	}
	if verified.Manifest.Arch != nodeArch {
		return fmt.Errorf("the build archive in %s is for linux/%s and this node is linux/%s, so the join was "+
			"not requested; build with --arch %s", archiveDir, verified.Manifest.Arch, nodeArch, nodeArch)
	}
	ps.logf("  ✓ Build archive v%s intact (signed by %s)", verified.Manifest.Version, verified.Signer)
	return nil
}

// VerifyPreBuiltArchive checks the archive extracted at OramaBase against the
// anchor — signature, every file, the node's architecture, and whether a
// signer rotation it carries would be accepted — and changes nothing. An
// upgrade calls it before it stops any service, so an archive Phase 2b would
// refuse never costs the node its uptime.
func (ps *ProductionSetup) VerifyPreBuiltArchive() error {
	if !HasPreBuiltArchive() {
		return fmt.Errorf("no build archive at %s (%s is missing); push one with `orama push`", archiveDir, OramaManifest)
	}
	unlock, err := lockArchive(archiveDir)
	if err != nil {
		return err
	}
	verified, err := verifyArchive(archiveSignersPath, archiveDir, nodeArch)
	if err = errors.Join(err, unlock()); err != nil {
		return fmt.Errorf("refusing the build archive in %s: %w", archiveDir, err)
	}
	ps.logf("  ✓ Build archive v%s verified (signed by %s)", verified.Manifest.Version, verified.Signer)
	return nil
}

// verifyPreBuiltArchive verifies the archive extracted at OramaBase against the
// anchor, applies a signed signer rotation, and returns the verified manifest.
// detected is the manifest Phase 2b found; it must be the one that verified.
// The caller holds the archive lock.
func (ps *ProductionSetup) verifyPreBuiltArchive(detected *PreBuiltManifest) (*PreBuiltManifest, error) {
	verified, rotated, err := verifyAndRotate(archiveSignersPath, archiveDir, nodeArch)
	if err != nil {
		return nil, fmt.Errorf("refusing to install the build archive in %s: %w", archiveDir, err)
	}
	if !reflect.DeepEqual(detected, verified.Manifest) {
		return nil, fmt.Errorf("refusing to install the build archive in %s: %s changed between detection "+
			"and verification", archiveDir, OramaManifest)
	}
	ps.logf("  ✓ Archive signature verified (signed by %s)", verified.Signer)
	if rotated {
		ps.logf("  ✓ Archive signers rotated: %s now trusts %s", archiveSignersPath, strings.Join(verified.Signers, ", "))
	}
	return verified.Manifest, nil
}
