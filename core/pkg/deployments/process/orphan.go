package process

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/deploysecrets"
)

// Finding and stopping runtime units no deployment owns.
//
// A delete stops its unit, but the stop can be refused or the node can be down,
// and the unit keeps running on a port the registry has already freed. Nothing
// else on the node lists units, so the health checker's orphan reaper
// (health/orphan_reaper.go) does, through these two methods.

// runtimeUnit matches the units that run a deployment's process, and not the
// oneshot build@ and clean@ units.
var runtimeUnit = regexp.MustCompile(`^orama-deploy-(` + string(RuntimeNode) + `|` + string(RuntimeNPM) + `|` + string(RuntimeGo) + `)@(.+)\.service$`)

// runtimeUnitGlob is what `systemctl list-units` is asked for.
const runtimeUnitGlob = UnitPrefix + "*@*.service"

// systemd properties of a unit that say when its current life began.
const (
	propActiveState    = "ActiveState"
	propActiveEnter    = "ActiveEnterTimestamp"
	propInactiveExit   = "InactiveExitTimestamp"
	propStateChange    = "StateChangeTimestamp"
	activeStateFailed  = "failed"
	systemdNoTimestamp = "n/a"
)

// RuntimeUnit is one deployment runtime unit on this node, in any state.
type RuntimeUnit struct {
	Unit     string
	Runtime  Runtime
	Instance string
	// Since is when the unit's current life began: its last activation, or for
	// a failed unit its last state change.
	Since time.Time
}

// ListRuntimeUnits lists this node's deployment runtime units: active,
// activating (auto-restart) and failed alike. Both queries are read-only and
// need no privilege, like is-active.
func (m *Manager) ListRuntimeUnits(ctx context.Context) ([]RuntimeUnit, error) {
	out, err := m.querySystemctl(ctx, "list-units", runtimeUnitGlob, "--all", "--plain", "--no-legend", "--no-pager")
	if err != nil {
		return nil, fmt.Errorf("list the deployment runtime units: %w", err)
	}
	var units []RuntimeUnit
	var errs []error
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		match := runtimeUnit.FindStringSubmatch(fields[0])
		if match == nil {
			continue
		}
		since, err := m.unitSince(ctx, fields[0])
		if err != nil {
			errs = append(errs, err)
			continue
		}
		units = append(units, RuntimeUnit{Unit: fields[0], Runtime: Runtime(match[1]), Instance: match[2], Since: since})
	}
	return units, errors.Join(errs...)
}

// buildUnit matches the oneshot units that install a deployment's dependencies
// and remove them.
var buildUnit = regexp.MustCompile(`^orama-deploy-(` + buildRuntime + `|` + cleanRuntime + `)@(.+)\.service$`)

// ActiveBuildInstances lists the instances whose build or clean unit is active,
// activating or failed (`systemctl list-units` without --all shows no other).
// A first deploy runs its build before its deployments row exists, so the
// instance has no row yet and its cache must not be taken for an orphan's.
func (m *Manager) ActiveBuildInstances(ctx context.Context) ([]string, error) {
	out, err := m.querySystemctl(ctx, "list-units", UnitPrefix+buildRuntime+"@*.service", UnitPrefix+cleanRuntime+"@*.service", "--plain", "--no-legend", "--no-pager")
	if err != nil {
		return nil, fmt.Errorf("list the deployment build units: %w", err)
	}
	var instances []string
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[2] == "inactive" {
			continue
		}
		if match := buildUnit.FindStringSubmatch(fields[0]); match != nil {
			instances = append(instances, match[2])
		}
	}
	return instances, nil
}

// unitSince is the start of a unit's current life. A unit that cannot be dated
// is an error, not "old": the reaper takes nothing it cannot date.
func (m *Manager) unitSince(ctx context.Context, unit string) (time.Time, error) {
	out, err := m.querySystemctl(ctx, "show", unit, "--timestamp=utc",
		"--property="+propActiveState, "--property="+propActiveEnter,
		"--property="+propInactiveExit, "--property="+propStateChange)
	if err != nil {
		return time.Time{}, fmt.Errorf("show %s: %w", unit, err)
	}
	props := parseSystemctlShow(string(out))
	keys := []string{propActiveEnter, propInactiveExit}
	if props[propActiveState] == activeStateFailed {
		keys = []string{propStateChange}
	}
	for _, key := range keys {
		if ts := props[key]; ts != "" && ts != systemdNoTimestamp {
			t, err := parseSystemdTimestamp(ts)
			if err != nil {
				return time.Time{}, fmt.Errorf("%s of %s: %w", key, unit, err)
			}
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("%s reports no %s", unit, strings.Join(keys, " or "))
}

func (m *Manager) querySystemctl(ctx context.Context, args ...string) ([]byte, error) {
	if m.query != nil {
		return m.query(ctx, args...)
	}
	return exec.CommandContext(ctx, "systemctl", args...).Output()
}

// StopOrphan stops the runtime unit of an instance no deployment owns and
// removes what it left: the staged environment and credential, and the build
// output its dependencies were installed into. It works from runtime and
// instance because the namespace and name cannot be told apart in an instance.
// Every step is attempted and every failure returned, as Stop does.
func (m *Manager) StopOrphan(runtime Runtime, instance string) error {
	if !deploysecrets.ValidInstance(instance) {
		return fmt.Errorf("%q is not a deployment instance", instance)
	}
	unit := fmt.Sprintf("%s%s@%s.service", UnitPrefix, runtime, instance)
	var errs []error
	stopErr := m.systemdStop(unit)
	if stopErr != nil {
		errs = append(errs, fmt.Errorf("stop %s: %w", unit, stopErr))
	}
	if err := m.systemdDisable(unit); err != nil {
		errs = append(errs, fmt.Errorf("disable %s: %w", unit, err))
	}
	// The removal of the build output runs as the instance's build user, whose
	// drop-in the secrets' removal deletes, so it comes first.
	if stopErr == nil && runtime != RuntimeGo {
		if err := m.giveBuildUser(instance); err != nil {
			errs = append(errs, err)
		} else if err := m.runOneshot(fmt.Sprintf("%s%s@%s.service", UnitPrefix, cleanRuntime, instance)); err != nil {
			errs = append(errs, fmt.Errorf("clear the installed dependencies of %s: %w", unit, err))
		}
	}
	if err := m.removeSecrets(UnitPrefix + instance); err != nil {
		errs = append(errs, fmt.Errorf("remove the secrets of %s, which are still on disk: %w", unit, err))
	}
	if stopErr == nil {
		if err := m.purgeState(instance); err != nil {
			errs = append(errs, fmt.Errorf("remove the state and cache directories of %s, which are still on disk: %w", unit, err))
		}
	}
	return errors.Join(errs...)
}

// PurgeOrphanState removes the state and cache directories of an instance no
// deployment owns and no unit runs.
func (m *Manager) PurgeOrphanState(instance string) error {
	if !deploysecrets.ValidInstance(instance) {
		return fmt.Errorf("%q is not a deployment instance", instance)
	}
	if m.stager == nil {
		return fmt.Errorf("no deployment stager is configured, so the directories of %s cannot be removed", instance)
	}
	return m.stager.PurgeState(instance)
}

// StateInstances lists the instances with a state or cache directory on this
// node.
func (m *Manager) StateInstances() ([]string, error) {
	if m.stager == nil {
		return nil, errors.New("no deployment stager is configured, so the node's deployment directories cannot be listed")
	}
	return m.stager.StateInstances()
}
