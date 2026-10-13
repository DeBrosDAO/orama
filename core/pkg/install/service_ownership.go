package install

import (
	"fmt"
	"io/fs"
	"os/user"
	"path/filepath"
	"strconv"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/systemd"
)

// sfuConfigGlob matches the configs the namespace spawner writes for the SFU,
// sfu-<node>.yaml in each namespace's configs/ directory (pkg/namespace
// SpawnSFU).
const sfuConfigGlob = "sfu-*.yaml"

// ownershipChange is the owner and mode one file in the orama tree must have.
type ownershipChange struct {
	path string
	uid  int
	gid  int
	mode fs.FileMode
}

// accountIDs resolves account names to ids; systemAccountIDs is the node's.
type accountIDs struct {
	uid func(name string) (int, error)
	gid func(name string) (int, error)
}

var systemAccountIDs = accountIDs{uid: lookupUID, gid: lookupGID}

func lookupUID(name string) (int, error) {
	u, err := user.Lookup(name)
	if err != nil {
		return 0, fmt.Errorf("look up the %s user: %w", name, err)
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return 0, fmt.Errorf("the %s user has a non-numeric uid %q: %w", name, u.Uid, err)
	}
	return uid, nil
}

func lookupGID(name string) (int, error) {
	g, err := user.LookupGroup(name)
	if err != nil {
		return 0, fmt.Errorf("look up the %s group: %w", name, err)
	}
	gid, err := strconv.Atoi(g.Gid)
	if err != nil {
		return 0, fmt.Errorf("the %s group has a non-numeric gid %q: %w", name, g.Gid, err)
	}
	return gid, nil
}

// serviceOwnershipPlan is what an isolated service must own or read in the
// orama tree: every SFU config already on the node, which chownOramaTree has
// just handed back to orama:orama, goes to orama:orama-sfu 0640. The spawner
// writes new ones that way itself.
func serviceOwnershipPlan(oramaDir string, ids accountIDs) ([]ownershipChange, error) {
	pattern := filepath.Join(constants.NamespacesDir(oramaDir), "*", "configs", sfuConfigGlob)
	configs, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("list the SFU configs (%s): %w", pattern, err)
	}
	if len(configs) == 0 {
		return nil, nil
	}
	uid, err := ids.uid(supervisorUser)
	if err != nil {
		return nil, err
	}
	sfuGroup, err := systemd.ServiceUser(string(systemd.ServiceTypeSFU), true)
	if err != nil {
		return nil, err
	}
	gid, err := ids.gid(sfuGroup)
	if err != nil {
		return nil, err
	}
	changes := make([]ownershipChange, 0, len(configs))
	for _, path := range configs {
		changes = append(changes, ownershipChange{path: path, uid: uid, gid: gid, mode: systemd.ServiceConfigMode})
	}
	return changes, nil
}

// ownershipSetter sets owner and mode without following a symlink; the orama
// tree's rootfs.Root is the real one.
type ownershipSetter interface {
	Chown(path string, uid, gid int) error
	Chmod(path string, mode fs.FileMode) error
}

// applyOwnership makes every change, owner before mode, and stops at the
// first failure.
func applyOwnership(files ownershipSetter, changes []ownershipChange) error {
	for _, c := range changes {
		if err := files.Chown(c.path, c.uid, c.gid); err != nil {
			return fmt.Errorf("hand %s to %d:%d: %w", c.path, c.uid, c.gid, err)
		}
		if err := files.Chmod(c.path, c.mode); err != nil {
			return fmt.Errorf("set %s to %04o: %w", c.path, c.mode, err)
		}
	}
	return nil
}

// applyServiceOwnership runs serviceOwnershipPlan on the node, through rootfs:
// the orama tree is the orama user's, and a symlink planted in it is refused,
// not followed.
func (ps *ProductionSetup) applyServiceOwnership() error {
	changes, err := serviceOwnershipPlan(ps.oramaDir, systemAccountIDs)
	if err != nil {
		return fmt.Errorf("plan the isolated services' file ownership: %w", err)
	}
	if err := applyOwnership(OramaRoot(ps.oramaDir), changes); err != nil {
		return fmt.Errorf("hand the isolated services their files: %w", err)
	}
	ps.logf("  ✓ Isolated services' files handed to their groups (%d)", len(changes))
	return nil
}
