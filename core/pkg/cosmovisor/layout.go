// Package cosmovisor lays out the chain binary the way cosmovisor runs it:
//
//	<home>/cosmovisor/genesis/bin/oramad
//	<home>/cosmovisor/upgrades/<name>/bin/oramad
//	<home>/cosmovisor/upgrades/<name>/upgrade-info.json -> ../../../cosmovisor-upgrade-info-<name>.json
//	<home>/cosmovisor/current -> genesis or upgrades/<name>
//
// Root stages binaries here, inside a home the chain account owns, so every
// operation is fd-relative and refuses symlinks: each directory is opened
// with O_NOFOLLOW from the one before it, checked for its owner and mode on
// the descriptor, and never looked up by path again. genesis/, upgrades/
// and everything below them are root's and not writable by anyone else.
// The chain account may write exactly what cosmovisor writes:
//
//   - current, which cosmovisor removes and re-creates in cosmovisor/ at the
//     upgrade height. cosmovisor/ is root-owned, group orama-chain, mode
//     1775: the sticky bit lets the chain account create and replace its own
//     entries there and never remove or rename root's.
//   - upgrade-info.json, which cosmovisor's SetCurrentUpgrade creates in the
//     upgrade's directory. Root places a symlink there pointing into the
//     chain home, so cosmovisor's write lands in the home and the upgrade
//     directory stays root's and read-only. Root never follows that link.
//
// Every binary is copied into a root-only staging directory and verified
// through the descriptor that wrote it before it is linked into place, so
// the bytes checked are the bytes installed. This package does not import
// cosmovisor and never repoints current once it exists.
package cosmovisor

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// Names inside DAEMON_HOME, fixed by cosmovisor.
const (
	rootDir         = "cosmovisor"
	genesisDir      = "genesis"
	upgradesDir     = "upgrades"
	binDir          = "bin"
	currentLink     = "current"
	upgradeInfoName = "upgrade-info.json"
	// upgradeInfoTargetFormat names the file in the chain home that an
	// upgrade's upgrade-info.json link points at.
	upgradeInfoTargetFormat = "cosmovisor-upgrade-info-%s.json"
	// upgradeInfoToHome climbs from upgrades/<name>/ to the chain home.
	upgradeInfoToHome = "../../../"
)

// upgradeName is a plan name cosmovisor maps to itself: cosmovisor
// lowercases the name and URI-escapes it, so only lowercase letters,
// digits, dot, dash and underscore name the same directory on both sides.
var upgradeName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// Verify checks a staged binary through the descriptor it was written with.
type Verify func(f *os.File) error

// Companion is a file that rides in a version's bin directory beside the
// daemon, so that version of the daemon runs the companion it was built with:
// the shielded verifier. It is staged and verified like the daemon.
type Companion struct {
	// Name is the file name in bin/. It is a plain name and not the daemon's.
	Name string
	// Src is the file to copy.
	Src    string
	Verify Verify
}

// Layout is one DAEMON_HOME and the account cosmovisor runs as.
type Layout struct {
	Home     string
	Daemon   string
	ChainUID int
	ChainGID int
	// trusted reports whether a component owned by uid is root's. nil is
	// uid 0; a test running unprivileged replaces it.
	trusted func(uid uint32, name string) bool
}

// Root is <home>/cosmovisor.
func (l Layout) Root() string { return filepath.Join(l.Home, rootDir) }

// GenesisBinary is the binary cosmovisor runs before any upgrade.
func (l Layout) GenesisBinary() string {
	return filepath.Join(l.Root(), genesisDir, binDir, l.Daemon)
}

// GenesisBinDir is the directory the genesis binary and its companions are in.
func (l Layout) GenesisBinDir() string { return filepath.Dir(l.GenesisBinary()) }

// CurrentBinDir is the bin directory of the version cosmovisor runs, through
// the current link. It is what a companion's path in a unit's arguments names,
// so an upgrade switches companions with the daemon.
func (l Layout) CurrentBinDir() string { return filepath.Join(l.Current(), binDir) }

// UpgradeBinary is the binary cosmovisor switches to for plan name.
func (l Layout) UpgradeBinary(name string) (string, error) {
	if err := checkUpgradeName(name); err != nil {
		return "", err
	}
	return filepath.Join(l.Root(), upgradesDir, name, binDir, l.Daemon), nil
}

// ReadOnlyDirs are the directories the chain unit mounts read-only:
// everything root stages. StageGenesis creates both.
func (l Layout) ReadOnlyDirs() []string {
	return []string{filepath.Join(l.Root(), genesisDir), filepath.Join(l.Root(), upgradesDir)}
}

// Current is the symlink cosmovisor runs through.
func (l Layout) Current() string { return filepath.Join(l.Root(), currentLink) }

// checkCompanion refuses a companion name that could name another file.
func (l Layout) checkCompanion(c Companion) error {
	if c.Name == "" || filepath.Base(c.Name) != c.Name || c.Name == "." || c.Name == ".." || c.Name == l.Daemon {
		return fmt.Errorf("companion %q must be a plain file name other than %s", c.Name, l.Daemon)
	}
	return nil
}

func checkUpgradeName(name string) error {
	if !upgradeName.MatchString(name) {
		return fmt.Errorf("upgrade name %q must be 1-64 lowercase letters, digits, '.', '-' or '_', starting with a letter or digit", name)
	}
	return nil
}

func (l Layout) check() error {
	if !filepath.IsAbs(l.Home) || filepath.Clean(l.Home) != l.Home {
		return fmt.Errorf("the chain home %q must be a clean absolute path", l.Home)
	}
	if l.Daemon == "" || filepath.Base(l.Daemon) != l.Daemon {
		return fmt.Errorf("the daemon name %q must be a single file name", l.Daemon)
	}
	if l.ChainUID <= 0 || l.ChainGID <= 0 {
		return fmt.Errorf("the chain account must be an unprivileged uid and gid, not %d:%d", l.ChainUID, l.ChainGID)
	}
	return nil
}

func (l Layout) isTrusted(uid uint32, name string) bool {
	if l.trusted != nil {
		return l.trusted(uid, name)
	}
	return uid == 0
}
