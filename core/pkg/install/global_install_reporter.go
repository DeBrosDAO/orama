package install

import (
	"fmt"
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
	// reporterVotesDir is where the reporter looks for the authority's archived votes.
	reporterVotesDir = "votes"
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
// mode 0700, the operator and authority-id files, and an empty votes directory
// for the archive. An existing hot key, state and report are left as they are.
func applyGlobalReporter(h GlobalHost, plan *reporterPlan) error {
	uid, gid, err := h.Lookup(globalReporterUser)
	if err != nil {
		return fmt.Errorf("look up the %s account: %w", globalReporterUser, err)
	}
	home := filepath.Join(h.StateDir, filepath.Base(constants.GlobalReporterHome))
	if err := ownedDir(h, home, uid, gid); err != nil {
		return err
	}
	if err := ownedDir(h, filepath.Join(home, reporterVotesDir), uid, gid); err != nil {
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
