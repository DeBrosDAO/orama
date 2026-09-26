package upgrade

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/DeBrosOfficial/network/pkg/legacylayout"
	"github.com/DeBrosOfficial/network/pkg/rootfs"
)

// devNull is what a masked unit's symlink points at.
const devNull = "/dev/null"

// retireLegacyDeploymentUnits removes the per-deployment units 0.122.x wrote
// to /etc/systemd/system (orama-deploy-<instance>.service): stopped, disabled
// and deleted, then one daemon-reload.
//
// It runs after orama-node is back and healthy, which is after its
// legacy-layout step moved each deployment's directory to
// data/deployments/<instance> and staged the environment the unit ran with. A
// legacy unit left behind runs from a directory that no longer exists on its
// next start, keeps the deployment's port from the template unit that replaces
// it, and keeps the tenant's secrets in a world-readable unit file.
//
// Nothing is walked: the directory is root's, only names matching the legacy
// pattern (legacylayout.LegacyDeploymentUnit — one fixed prefix, an instance
// of the characters orama-privhelper accepts, no '@', no '/', no '.') are
// touched, and each is removed by that name in that directory. A symlink is
// accepted only when it is a mask (to /dev/null), which `systemctl unmask`
// removes.
func retireLegacyDeploymentUnits() error {
	return retireLegacyDeploymentUnitsIn(hostUnitDir, runSystemctl)
}

// runSystemctl runs systemctl with args, returning its output on failure.
func runSystemctl(args ...string) error {
	out, err := exec.Command("systemctl", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl %v: %w (%s)", args, err, out)
	}
	return nil
}

func retireLegacyDeploymentUnitsIn(dir string, systemctl func(args ...string) error) error {
	units, err := legacyDeploymentUnits(dir)
	if err != nil {
		return err
	}
	if len(units) == 0 {
		fmt.Printf("  No 0.122.x deployment units on this node\n")
		return nil
	}
	root := rootfs.At(dir)
	for _, u := range units {
		if err := systemctl("stop", u.name); err != nil {
			return err
		}
		if err := systemctl("disable", u.name); err != nil {
			return err
		}
		if err := removeUnit(root, dir, u, systemctl); err != nil {
			return err
		}
		fmt.Printf("  ✓ Retired %s\n", u.name)
	}
	return systemctl("daemon-reload")
}

// removeUnit deletes a legacy unit: a mask is systemd's to remove (`unmask`
// deletes exactly that /dev/null symlink); a unit file is removed through
// rootfs, which refuses a symlink.
func removeUnit(root rootfs.Root, dir string, u legacyUnit, systemctl func(args ...string) error) error {
	if u.masked {
		return systemctl("unmask", u.name)
	}
	if err := root.Remove(filepath.Join(dir, u.name)); err != nil {
		return fmt.Errorf("remove %s: %w", filepath.Join(dir, u.name), err)
	}
	return nil
}

// legacyUnit is one legacy per-deployment unit in the unit directory.
type legacyUnit struct {
	name   string
	masked bool
}

// legacyDeploymentUnits lists the legacy per-deployment units in dir.
func legacyDeploymentUnits(dir string) ([]legacyUnit, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", dir, err)
	}
	var units []legacyUnit
	for _, e := range entries {
		if _, ok := legacylayout.LegacyDeploymentUnit(e.Name()); !ok {
			continue
		}
		masked, err := classifyUnit(dir, e)
		if err != nil {
			return nil, err
		}
		units = append(units, legacyUnit{name: e.Name(), masked: masked})
	}
	return units, nil
}

// classifyUnit accepts a regular unit file or a mask (reporting which), and
// refuses anything else under a legacy unit's name.
func classifyUnit(dir string, e fs.DirEntry) (masked bool, err error) {
	path := filepath.Join(dir, e.Name())
	switch {
	case e.Type().IsRegular():
		return false, nil
	case e.Type()&fs.ModeSymlink != 0:
		target, err := os.Readlink(path)
		if err != nil {
			return false, fmt.Errorf("read the symlink %s: %w", path, err)
		}
		if target == devNull {
			return true, nil
		}
		return false, fmt.Errorf("%s is a symlink to %s, which no release wrote; remove it by hand", path, target)
	default:
		return false, errors.New(path + " is not a unit file; remove it by hand")
	}
}
