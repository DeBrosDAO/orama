package upgrade

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	oramainstall "github.com/DeBrosOfficial/network/pkg/install"
	"github.com/DeBrosOfficial/network/pkg/install/installers"
)

// shutdownDrainPause lets sockets close after systemd has stopped the services
// that held them.
const shutdownDrainPause = 3 * time.Second

// supervisorUnit is orama-node, which starts every orama-namespace-*@index
// unit (and on 0.122.x ran rqlited itself).
const supervisorUnit = "orama-node.service"

// namespaceUnitGlob matches every namespace instance unit.
const namespaceUnitGlob = "orama-namespace-*@*.service"

// hostUnitDir is where the host units live.
const hostUnitDir = "/etc/systemd/system"

// stopServices stops everything that runs from the binaries Phase 2b is
// about to replace, or holds a port the upgraded node binds: the supervisor
// first, so it cannot start again what is being stopped, then every namespace
// unit systemd has loaded, then 0.122.x's per-deployment units, then the host
// daemons older installs wrote.
//
// Every stop is fatal. A unit left running would run the old binary against
// the new configs, or hold a port the new units need.
//
// There is no peers.json here. The upgrade used to write a recovery peers.json
// from /nodes before stopping, taking each member's raft id for its address and
// leaving the non-voters out: with raft ids that are peer ids, every upgraded
// node restarted into a configuration of addresses that do not exist. A
// restarting member rejoins from its own raft state; a node that lost that
// state rejoins through its membership record (pkg/namespace indexJoinTargets).
func (o *Orchestrator) stopServices() error {
	fmt.Printf("\n⏹️  Stopping all services before upgrade...\n")
	units := o.units
	if units == nil {
		units = systemdUnits{ctl: oramainstall.NewSystemdController()}
	}

	if err := stopIfPresent(units, supervisorUnit); err != nil {
		return err
	}
	namespaceUnits, err := units.loadedNamespaceUnits()
	if err != nil {
		return err
	}
	for _, unit := range namespaceUnits {
		if err := units.stop(unit); err != nil {
			return err
		}
	}
	fmt.Printf("  ✓ Stopped %d namespace unit(s)\n", len(namespaceUnits))
	// 0.122.x's per-deployment units: stopped now, deleted once the node is
	// back (retireLegacyDeploymentUnits). Left running they would hold the
	// deployment's port against the template unit that replaces them and
	// crash-loop from the directory orama-node moves away.
	deployUnits, err := units.legacyDeploymentUnits()
	if err != nil {
		return err
	}
	for _, unit := range deployUnits {
		if err := units.stop(unit); err != nil {
			return err
		}
	}
	if len(deployUnits) > 0 {
		fmt.Printf("  ✓ Stopped %d 0.122.x deployment unit(s)\n", len(deployUnits))
	}
	for _, unit := range installers.LegacyHostUnits {
		if err := stopIfPresent(units, unit); err != nil {
			return err
		}
	}
	// A deliberate drain pause, not a readiness wait: systemd has already
	// returned from each stop, and this gives sockets a moment to close before
	// the next phase binds them.
	time.Sleep(drainPause)
	return nil
}

// drainPause is shutdownDrainPause; a test sets it to zero.
var drainPause = shutdownDrainPause

// unitController is what stopServices does to systemd.
type unitController interface {
	stop(unit string) error
	unitFileExists(unit string) (bool, error)
	loadedNamespaceUnits() ([]string, error)
	legacyDeploymentUnits() ([]string, error)
}

// systemdUnits is the node's systemd.
type systemdUnits struct {
	ctl *oramainstall.SystemdController
}

func (u systemdUnits) stop(unit string) error { return u.ctl.StopService(unit) }

func (u systemdUnits) unitFileExists(unit string) (bool, error) {
	_, err := os.Stat(filepath.Join(hostUnitDir, unit))
	switch {
	case err == nil:
		return true, nil
	case os.IsNotExist(err):
		return false, nil
	default:
		return false, fmt.Errorf("inspect %s: %w", unit, err)
	}
}

func (u systemdUnits) loadedNamespaceUnits() ([]string, error) { return loadedNamespaceUnits() }

func (u systemdUnits) legacyDeploymentUnits() ([]string, error) {
	units, err := legacyDeploymentUnits(hostUnitDir)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(units))
	for _, unit := range units {
		names = append(names, unit.name)
	}
	return names, nil
}

// stopIfPresent stops unit when its unit file exists.
func stopIfPresent(units unitController, unit string) error {
	present, err := units.unitFileExists(unit)
	if err != nil || !present {
		return err
	}
	if err := units.stop(unit); err != nil {
		return err
	}
	fmt.Printf("  ✓ Stopped %s\n", unit)
	return nil
}

// loadedNamespaceUnits lists every namespace unit systemd has loaded, in any
// state. Listing only running ones missed a unit between two restarts of a
// crash loop — "activating (auto-restart)" — which systemd then started again
// in the middle of the upgrade, on the old binary.
func loadedNamespaceUnits() ([]string, error) {
	out, err := exec.Command("systemctl", "list-units", "--all", "--type=service",
		"--no-pager", "--no-legend", "--plain", namespaceUnitGlob).Output()
	if err != nil {
		return nil, fmt.Errorf("list the namespace units: %w", err)
	}
	return parseUnitList(string(out)), nil
}

// parseUnitList reads the unit names out of `systemctl list-units --plain
// --no-legend` output: the first field of each line.
func parseUnitList(out string) []string {
	var units []string
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 && strings.HasPrefix(fields[0], "orama-namespace-") {
			units = append(units, fields[0])
		}
	}
	return units
}
