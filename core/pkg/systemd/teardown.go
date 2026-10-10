package systemd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/unitenv"
	"go.uber.org/zap"
)

// tenantTeardownOrder is every service a tenant namespace can run, dependents
// first: SFU and TURN, then Gateway, Olric, RQLite.
var tenantTeardownOrder = []ServiceType{
	ServiceTypeSFU, ServiceTypeTURN, ServiceTypeGateway, ServiceTypeOlric, ServiceTypeRQLite,
}

// TeardownService stops a namespace service AND disables it.
//
// StopService alone is for a restart: the unit stays enabled, so the next
// `orama node upgrade` (which enables and restarts every discovered namespace
// unit) or boot brings it straight back. A namespace that is going away must
// not be brought back, so its units are disabled as they are stopped.
//
// A unit that failed stays loaded in the failed state, listed by systemctl and
// never garbage-collected, until its failed state is reset: stopping and
// disabling it leaves it there. The teardown resets it, so a removed namespace
// leaves no unit behind.
func (m *Manager) TeardownService(namespace string, serviceType ServiceType) error {
	if err := m.StopService(namespace, serviceType); err != nil {
		return err
	}
	if err := m.DisableService(namespace, serviceType); err != nil {
		return err
	}
	return m.resetFailedUnit(m.serviceName(namespace, serviceType))
}

// resetFailedUnit clears the failed state of a unit that has been stopped and
// disabled. systemd only has a unit in memory to reset while it is failed (or
// running), so `systemctl reset-failed` on a stopped unit that did not fail
// exits 1 with "not loaded". That is not a failure: LoadState cannot tell the
// two apart, because systemd reports every instance of an installed template
// as loaded, so the reset has failed only if the unit is still in the failed
// state afterwards.
func (m *Manager) resetFailedUnit(unit string) error {
	output, err := m.runUnit("reset-failed", unit)
	if err == nil {
		return nil
	}
	state, stateErr := m.readUnitState(unit)
	if stateErr != nil {
		return fmt.Errorf("failed to reset the failed state of %s: %w; output: %s (and its state could not be read: %v)", unit, err, string(output), stateErr)
	}
	if state.Active != activeStateFailed {
		return nil
	}
	return fmt.Errorf("failed to reset the failed state of %s: %w; output: %s", unit, err, string(output))
}

// TeardownServiceAndEnv retires one service of a namespace that stays: it is
// stopped, disabled and its env file removed. The env file is what `orama node
// upgrade` discovers the service from, so with it gone nothing restarts the
// service. The env file is kept when the stop or disable failed — it is the
// retry handle, and the failure is returned.
func (m *Manager) TeardownServiceAndEnv(namespace string, serviceType ServiceType) error {
	if err := m.TeardownService(namespace, serviceType); err != nil {
		return err
	}
	// The namespace stays, so nothing later reloads systemd: reload here or the
	// retired instance's cached UnitFileState keeps reading as enabled.
	if err := m.ReloadDaemon(); err != nil {
		return fmt.Errorf("reload systemd after retiring %s: %w", m.serviceName(namespace, serviceType), err)
	}
	if err := m.clearUnitEnv(namespace, string(serviceType)); err != nil {
		return fmt.Errorf("remove the env file of %s: %w", m.serviceName(namespace, serviceType), err)
	}
	return nil
}

// TeardownAllNamespaceServices stops and disables every tenant service of a
// namespace, and removes its deployment units. Unlike StopAllNamespaceServices
// it attempts every service and returns every failure: a unit that could not
// be disabled is a unit that can come back, and the caller has to know.
func (m *Manager) TeardownAllNamespaceServices(namespace string) error {
	m.logger.Info("Tearing down all namespace services", zap.String("namespace", namespace))

	var errs []error
	if err := m.StopDeploymentServicesForNamespace(namespace); err != nil {
		errs = append(errs, fmt.Errorf("tear down the deployment units of namespace %s: %w", namespace, err))
	}
	for _, st := range tenantTeardownOrder {
		if err := m.TeardownService(namespace, st); err != nil {
			errs = append(errs, err)
		}
	}
	// One reload for the namespace, not one per service: systemd caches a
	// loaded unit's UnitFileState, so an instance it still remembers (failed)
	// would read as enabled after a --no-reload disable and LocalTenantNamespaces
	// would keep counting the namespace until the next reload.
	if err := m.ReloadDaemon(); err != nil {
		errs = append(errs, fmt.Errorf("reload systemd after tearing down namespace %s: %w", namespace, err))
	}
	return errors.Join(errs...)
}

// LocalTenantNamespaces returns the tenant namespaces that have state on this
// node: a data directory with a provisioned tenant service (its unit env file
// exists), or a tenant unit instance systemd has loaded. These are the
// namespaces `orama node upgrade` and a reboot would start.
func (m *Manager) LocalTenantNamespaces() ([]string, error) {
	found, err := m.tenantNamespacesOnDisk()
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(found))
	for _, ns := range found {
		seen[ns] = true
	}

	units, err := m.ListNamespaceServices()
	if err != nil {
		return nil, err
	}
	for _, u := range units {
		ns, ok := TenantNamespaceFromUnit(u)
		if !ok || seen[ns] {
			continue
		}
		// systemd keeps listing an instance it has seen after it was stopped
		// and disabled; such a unit cannot start again, so it is not a
		// namespace on this node. Counting it made the orphan sweep tear the
		// same removed namespace down on every pass and spend its cap on it.
		st, err := m.readUnitState(u)
		if err != nil {
			return nil, fmt.Errorf("read the state of %s: %w", u, err)
		}
		if !st.live() {
			continue
		}
		seen[ns] = true
		found = append(found, ns)
	}
	return found, nil
}

// tenantNamespacesOnDisk lists the namespace directories that hold a
// provisioned tenant service. This is what discoverNamespaceUnits in the CLI
// restarts on upgrade.
func (m *Manager) tenantNamespacesOnDisk() ([]string, error) {
	dir := m.unitEnvDir
	if dir == "" {
		dir = unitenv.Dir
	}
	entries, err := os.ReadDir(m.namespaceBase)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read namespaces directory %s: %w", m.namespaceBase, err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && hasTenantEnv(dir, e.Name()) {
			out = append(out, e.Name())
		}
	}
	return out, nil
}

func hasTenantEnv(envDir, namespace string) bool {
	for _, st := range tenantTeardownOrder {
		if _, err := os.Stat(unitenv.Path(envDir, namespace, string(st))); err == nil {
			return true
		}
	}
	return false
}

// TenantNamespaceFromUnit returns the namespace of a tenant unit instance such
// as "orama-namespace-rqlite@acme.service". Units of other services
// (wireguard@index, ipfs@index, ...) are not tenant units.
func TenantNamespaceFromUnit(unit string) (string, bool) {
	rest, ok := strings.CutPrefix(unit, "orama-namespace-")
	if !ok {
		return "", false
	}
	svc, ns, ok := strings.Cut(rest, "@")
	if !ok {
		return "", false
	}
	ns = strings.TrimSuffix(ns, ".service")
	if ns == "" || filepath.Base(ns) != ns {
		return "", false
	}
	for _, st := range tenantTeardownOrder {
		if string(st) == svc {
			return ns, true
		}
	}
	return "", false
}
