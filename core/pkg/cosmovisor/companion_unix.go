//go:build unix

package cosmovisor

import (
	"errors"
	"path/filepath"
)

// StageGenesisCompanions places files beside a genesis binary that is already
// staged: a node installed before its release shipped a companion (the
// shielded verifier) gets it without its oramad being replaced. A file already
// there is refused, as for any staged file.
func (l Layout) StageGenesisCompanions(companions ...Companion) (err error) {
	homeFD, rootFD, err := l.openTop()
	if err != nil {
		return err
	}
	genesisFD, binFD := -1, -1
	defer func() { err = errors.Join(err, closeAll(homeFD, rootFD, genesisFD, binFD)) }()
	if genesisFD, err = l.openDir(rootFD, genesisDir, false, false); err != nil {
		return err
	}
	if binFD, err = l.openDir(genesisFD, binDir, false, false); err != nil {
		return err
	}
	for _, c := range companions {
		if err := l.checkCompanion(c); err != nil {
			return err
		}
		if err := l.placeFile(rootFD, binFD, c.Name, c.Src, filepath.Join(l.GenesisBinDir(), c.Name), c.Verify); err != nil {
			return err
		}
	}
	return nil
}

// placeAll stages the companions, then the daemon, into binFD. The daemon goes
// last: cosmovisor treats a directory with its binary as a staged version, so a
// failure part way never leaves one that lacks a companion. Whatever was placed
// before a failure is removed.
func (l Layout) placeAll(rootFD, binFD int, src, dst string, verify Verify, companions []Companion) (err error) {
	var placed []string
	defer func() {
		if err != nil {
			for _, name := range placed {
				err = errors.Join(err, unlinkIfPresent(binFD, name))
			}
		}
	}()
	for _, c := range companions {
		if err := l.checkCompanion(c); err != nil {
			return err
		}
		if err := l.placeFile(rootFD, binFD, c.Name, c.Src, filepath.Join(filepath.Dir(dst), c.Name), c.Verify); err != nil {
			return err
		}
		placed = append(placed, c.Name)
	}
	return l.placeFile(rootFD, binFD, l.Daemon, src, dst, verify)
}
