package systemd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"go.uber.org/zap"
)

// A tenant's deployments run as instances of per-runtime templates,
// orama-deploy-{runtime}@{instance}.service, where the instance is
// "<namespace>-<name>" with dots turned into hyphens. That name cannot be split
// back into its namespace: namespace "acme" with deployment "corp-web" and
// namespace "acme-corp" with deployment "web" are both "acme-corp-web". So the
// namespace's units are found through the owner marker every deployment
// directory carries (pkg/gateway/handlers/deployments instance_claim.go), which
// names the namespace exactly, and never through a glob on the namespace name.
const (
	deployUnitPrefix = "orama-deploy-"

	// deployOwnerMarkerName is the file in a deployment directory that names
	// its owner.
	deployOwnerMarkerName = ".orama-owner"

	// deployOwnerMarkerMaxBytes bounds a marker read: two short names in JSON.
	deployOwnerMarkerMaxBytes = 4096
)

// deployOwnerMarker is the marker's JSON shape.
type deployOwnerMarker struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

// deploymentsDir is the directory holding one directory per deployment, beside
// the namespaces directory.
func (m *Manager) deploymentsDir() string {
	if m.deploymentsBase != "" {
		return m.deploymentsBase
	}
	return filepath.Join(filepath.Dir(m.namespaceBase), "deployments")
}

// OwnedDeploymentInstances returns the instances of the deployments that belong
// to namespace, read from the owner markers under the deployments directory. A
// directory without a readable marker belongs to nobody that can be named and
// is never returned.
func (m *Manager) OwnedDeploymentInstances(namespace string) ([]string, error) {
	entries, err := os.ReadDir(m.deploymentsDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read the deployments directory %s: %w", m.deploymentsDir(), err)
	}
	var owned []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		marker, err := readDeployOwnerMarker(filepath.Join(m.deploymentsDir(), e.Name()))
		switch {
		case os.IsNotExist(err):
			m.logger.Debug("Deployment directory has no owner marker; leaving it alone", zap.String("instance", e.Name()))
		case err != nil:
			return nil, fmt.Errorf("read the owner of deployment %s: %w", e.Name(), err)
		case marker.Namespace == namespace:
			owned = append(owned, e.Name())
		}
	}
	return owned, nil
}

func readDeployOwnerMarker(dir string) (deployOwnerMarker, error) {
	f, err := os.Open(filepath.Join(dir, deployOwnerMarkerName))
	if err != nil {
		return deployOwnerMarker{}, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, deployOwnerMarkerMaxBytes))
	if err != nil {
		return deployOwnerMarker{}, err
	}
	var marker deployOwnerMarker
	if err := json.Unmarshal(raw, &marker); err != nil {
		return deployOwnerMarker{}, fmt.Errorf("parse owner marker: %w", err)
	}
	return marker, nil
}

// StopDeploymentServicesForNamespace stops and disables the deployment units of
// the namespace's own deployments, and removes their unit files. It touches
// exactly the instances the namespace owns (OwnedDeploymentInstances): tearing
// down "acme" leaves the deployments of "acme-corp" running.
//
// Every failure is returned: a deployment unit that could not be stopped or
// disabled is a process that survives the namespace.
func (m *Manager) StopDeploymentServicesForNamespace(namespace string) error {
	instances, err := m.OwnedDeploymentInstances(namespace)
	if err != nil {
		return err
	}
	var errs []error
	touched := 0
	for _, instance := range instances {
		units, err := m.listDeploymentUnits(instance)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, unit := range units {
			if err := m.removeDeploymentUnit(unit); err != nil {
				errs = append(errs, err)
			}
			touched++
		}
	}
	if touched > 0 {
		if err := m.ReloadDaemon(); err != nil {
			errs = append(errs, fmt.Errorf("reload systemd after removing deployment units: %w", err))
		}
		m.logger.Info("Deployment services cleanup complete",
			zap.String("namespace", namespace), zap.Int("units", touched))
	}
	return errors.Join(errs...)
}

// listDeploymentUnits names the loaded units of one deployment instance, one
// per runtime template. The instance is matched whole: the pattern ends at
// "@<instance>.service".
func (m *Manager) listDeploymentUnits(instance string) ([]string, error) {
	pattern := fmt.Sprintf("%s*@%s.service", deployUnitPrefix, instance)
	out, err := m.listUnits("--type=service", "--all", "--no-pager", "--no-legend", "--plain", pattern)
	if err != nil {
		return nil, fmt.Errorf("list the units of deployment %s: %w; output: %s", instance, err, strings.TrimSpace(string(out)))
	}
	var units []string
	for _, line := range strings.Split(string(out), "\n") {
		if fields := strings.Fields(line); len(fields) > 0 {
			units = append(units, fields[0])
		}
	}
	return units, nil
}

// listUnits runs `systemctl list-units`, a query that needs no privilege,
// through the listUnitsCmd seam.
func (m *Manager) listUnits(args ...string) ([]byte, error) {
	if m.listUnitsCmd != nil {
		return m.listUnitsCmd(args...)
	}
	return exec.Command("systemctl", append([]string{"list-units"}, args...)...).CombinedOutput()
}

// removeDeploymentUnit stops and disables one deployment unit and removes its
// unit file if it has one of its own.
func (m *Manager) removeDeploymentUnit(unit string) error {
	if err := m.stopUnit(unit); err != nil {
		return err
	}
	if err := m.disableUnit(unit); err != nil {
		return err
	}
	file := filepath.Join(m.systemdDir, unit)
	if err := os.Remove(file); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove the unit file %s: %w", file, err)
	}
	return nil
}
