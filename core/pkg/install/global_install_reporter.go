package install

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"slices"

	"github.com/DeBrosOfficial/network/pkg/constants"
)

// The files `orama-global reporter` reads in its home (chain/cmd/orama-global/reporter.go).
// The chain module cannot import this one, so the names are written in both
// and a test holds them together. The hot key is not here: the reporter
// creates it on its first start and an install never touches it.
const (
	reporterOperatorFile  = "operator"
	reporterAuthorityFile = "authority-id"
	// votesDirMode is the votes directory: the authority's account writes, the
	// reporter's group reads and enters, nobody else does, and a file made in it
	// takes the reporter's group (setgid), so the archive oneshot, which cannot
	// change a file's group to one it is not in, hands the reporter its votes.
	votesDirMode = 0o750 | fs.ModeSetgid
)

// operatorAddress is the shape of an account address, which is written into a
// file as it is: a bech32 address uses the alphabet without 1, b, i and o after
// the "1" separator. The reporter checks the checksum when it starts.
var operatorAddress = regexp.MustCompile(`^orama1[02-9ac-hj-np-z]{20,100}$`)

// reporterPlan is what the reporter's home holds, decided before the host changes.
type reporterPlan struct {
	// authorityID is the v3 identity of this host's directory authority in the
	// network file, the dir-source of its votes.
	authorityID string
	operator    string
}

// planGlobalReporter derives the authority-id from the network file: the
// reporter is in the chain module and does not import the parser. It returns
// nil when the install has no reporter. The authority is the one published at
// --tor-address, which the dirauth role of this install has already matched
// against its key bundle.
func planGlobalReporter(opts GlobalInstallOptions, tor *torPlan) (*reporterPlan, error) {
	if tor == nil || !slices.Contains(opts.Services, GlobalServiceReporter) {
		return nil, nil
	}
	auth, ok := tor.network.AuthorityAt(opts.Tor.Address)
	if !ok {
		return nil, fmt.Errorf("--tor-address %s is not a directory authority of network %s, so the reporter has no authority identity to report for", opts.Tor.Address, tor.network.Name)
	}
	return &reporterPlan{authorityID: auth.V3Ident, operator: opts.Tor.ReporterOperator}, nil
}

// applyGlobalReporter prepares the reporter's home: owned by its account with
// mode 0700, and the operator and authority-id files. An existing hot key,
// state and report are left as they are. It also makes the votes directory the
// authority's archive oneshot writes its own vote to (see votesDirMode).
func applyGlobalReporter(h GlobalHost, plan *reporterPlan) error {
	uid, gid, err := h.Lookup(globalReporterUser)
	if err != nil {
		return fmt.Errorf("look up the %s account: %w", globalReporterUser, err)
	}
	home := filepath.Join(h.StateDir, filepath.Base(constants.GlobalReporterHome))
	if err := ownedDir(h, home, uid, gid); err != nil {
		return err
	}
	if err := votesDir(h); err != nil {
		return err
	}
	for _, f := range []struct{ name, value string }{{reporterOperatorFile, plan.operator}, {reporterAuthorityFile, plan.authorityID}} {
		if err := ownedFile(h, filepath.Join(home, f.name), []byte(f.value+"\n"), uid, gid); err != nil {
			return err
		}
	}
	h.Logf("  ✓ %s configured for authority %s", constants.GlobalReporterHome, plan.authorityID)
	return nil
}

// votesDir makes the directory the authority's oneshot writes its vote to and
// the reporter reads: the authority's account owns it, the reporter's group
// reads it. Neither account is given anything of the other's home.
func votesDir(h GlobalHost) error {
	authorityUID, _, err := h.Lookup(globalTorDirauthUser)
	if err != nil {
		return fmt.Errorf("look up the %s account: %w", globalTorDirauthUser, err)
	}
	reporterGID, err := h.LookupGroup(globalReporterUser)
	if err != nil {
		return fmt.Errorf("look up the %s group: %w", globalReporterUser, err)
	}
	dir := filepath.Join(h.StateDir, filepath.Base(constants.GlobalTorVotesDir))
	if err := h.StateRoot.MkdirAll(dir, votesDirMode.Perm()); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	if err := h.Chown(h.StateRoot, dir, authorityUID, reporterGID); err != nil {
		return fmt.Errorf("chown %s: %w", dir, err)
	}
	// After the chown, which clears the setgid bit on some systems.
	if err := h.StateRoot.Chmod(dir, votesDirMode); err != nil {
		return fmt.Errorf("chmod %s: %w", dir, err)
	}
	return nil
}
