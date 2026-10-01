package health

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/DeBrosOfficial/network/pkg/deployments/process"
	"go.uber.org/zap"
)

// Reaping runtime units no deployment owns.
//
// A delete whose stop was refused, or that ran while this node was down, leaves
// the unit running on a port the registry has freed; the port allocator hands
// it to the next deployment, whose process dies on EADDRINUSE. The namespace
// orphan sweep cannot see these (a deployment's node is often not a member of
// its namespace's cluster) and the checker only walks rows that exist, so this
// sweep lists the node's units and compares them with the registry.
//
// Every doubt means nothing is stopped.

const (
	// orphanSweepInterval is how often the node's units are compared with the
	// registry. A unit is taken on the second sweep that finds it.
	orphanSweepInterval = 2 * time.Minute
	// orphanMinAge is how old a unit's current life must be before it can be
	// taken. A create writes the deployment row around the runtime start, so a
	// unit that has just started may simply be ahead of its row.
	orphanMinAge = 10 * time.Minute
	// maxOrphansPerSweep bounds what one sweep may stop.
	maxOrphansPerSweep = 2
	// orphanSweepTimeout bounds one sweep; it is shorter than the interval.
	orphanSweepTimeout = 90 * time.Second
)

// RuntimeUnitManager is what the reaper needs from the process manager.
type RuntimeUnitManager interface {
	ListRuntimeUnits(ctx context.Context) ([]process.RuntimeUnit, error)
	StopOrphan(runtime process.Runtime, instance string) error
}

// deploymentKey is the identity of a registry row.
type deploymentKey struct {
	Namespace string `db:"namespace"`
	Name      string `db:"name"`
}

// SetOrphanReaper enables the orphan unit sweep (optional). baseDeployPath is
// where deployments are extracted ("" leaves an orphan's files alone). Must be
// called before Start().
func (hc *HealthChecker) SetOrphanReaper(units RuntimeUnitManager, baseDeployPath string) {
	hc.orphanUnits = units
	hc.orphanDeployPath = baseDeployPath
}

// runOrphanSweeps sweeps on its own ticker, so listing and stopping units
// never delays the replica probes; each sweep is bounded by
// orphanSweepTimeout, which is shorter than the interval, so sweeps never
// overlap.
func (hc *HealthChecker) runOrphanSweeps(ctx context.Context) {
	ticker := time.NewTicker(orphanSweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sweepCtx, cancel := context.WithTimeout(ctx, orphanSweepTimeout)
			if err := hc.reapOrphanUnits(sweepCtx); err != nil {
				hc.logger.Error("Orphan unit sweep failed", zap.Error(err))
			}
			cancel()
		}
	}
}

// removeOrphanFiles removes a stopped orphan's extracted code, so a later
// deployment given the same instance does not start on stale files. No row
// owns the instance (that is what made it an orphan), so no deployment's
// claim on the directory is taken.
func (hc *HealthChecker) removeOrphanFiles(instance string) error {
	if hc.orphanDeployPath == "" {
		return nil
	}
	dir := filepath.Join(hc.orphanDeployPath, instance)
	if filepath.Dir(dir) != filepath.Clean(hc.orphanDeployPath) {
		return fmt.Errorf("instance %q does not name a directory directly under %s", instance, hc.orphanDeployPath)
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("remove the orphan's directory %s: %w", dir, err)
	}
	return nil
}

func (hc *HealthChecker) clock() time.Time {
	if hc.now != nil {
		return hc.now()
	}
	return time.Now()
}

// reapOrphanUnits is one sweep. It returns every stop that failed.
func (hc *HealthChecker) reapOrphanUnits(ctx context.Context) error {
	expected, err := hc.registeredInstances(ctx)
	if err != nil {
		hc.orphanSeen = nil
		return err
	}
	units, listErr := hc.orphanUnits.ListRuntimeUnits(ctx)

	now := hc.clock()
	seen := make(map[string]time.Time)
	var errs []error
	if listErr != nil {
		errs = append(errs, listErr)
	}
	stopped := 0
	for _, u := range units {
		if expected[u.Instance] {
			continue
		}
		first, wasSeen := hc.orphanSeen[u.Unit]
		if !wasSeen {
			first = now
		}
		seen[u.Unit] = first
		// A crash-looping unit restarts too often for its systemd age to
		// grow, so how long it has been a candidate counts as well.
		age := max(now.Sub(u.Since), now.Sub(first))
		if !wasSeen || age < orphanMinAge || stopped >= maxOrphansPerSweep {
			continue
		}
		stopped++
		delete(seen, u.Unit)
		hc.logger.Warn("Stopping a deployment unit no deployment owns",
			zap.String("unit", u.Unit),
			zap.String("instance", u.Instance),
			zap.String("reason", "no deployments row has this instance; seen on two consecutive sweeps and a candidate or running for longer than the minimum age"),
		)
		if err := hc.orphanUnits.StopOrphan(u.Runtime, u.Instance); err != nil {
			errs = append(errs, fmt.Errorf("stop the orphan unit %s: %w", u.Unit, err))
			continue
		}
		if err := hc.removeOrphanFiles(u.Instance); err != nil {
			errs = append(errs, err)
		}
	}
	hc.orphanSeen = seen
	return errors.Join(errs...)
}

// registeredInstances is the instance of every deployment row, in any status.
// An empty registry is an error: it is as likely a lagging database as a node
// with nothing deployed, and neither is proof that a unit is an orphan.
func (hc *HealthChecker) registeredInstances(ctx context.Context) (map[string]bool, error) {
	var rows []deploymentKey
	if err := hc.db.Query(ctx, &rows, `SELECT namespace, name FROM deployments`); err != nil {
		return nil, fmt.Errorf("read the deployments registry for the orphan sweep: %w", err)
	}
	if len(rows) == 0 {
		return nil, errors.New("the deployments registry has no rows; not treating any unit as an orphan")
	}
	expected := make(map[string]bool, len(rows))
	for _, r := range rows {
		expected[process.InstanceName(r.Namespace, r.Name)] = true
	}
	return expected, nil
}
