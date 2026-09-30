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
func (m *Manager) TeardownService(namespace string, serviceType ServiceType) error {
	if err := m.StopService(namespace, serviceType); err != nil {
		return err
	}
	return m.DisableService(namespace, serviceType)
}

// TeardownAllNamespaceServices stops and disables every tenant service of a
// namespace, and removes its deployment units. Unlike StopAllNamespaceServices
// it attempts every service and returns every failure: a unit that could not
// be disabled is a unit that can come back, and the caller has to know.
func (m *Manager) TeardownAllNamespaceServices(namespace string) error {
	m.logger.Info("Tearing down all namespace services", zap.String("namespace", namespace))

	m.StopDeploymentServicesForNamespace(namespace)

	var errs []error
	for _, st := range tenantTeardownOrder {
		if err := m.TeardownService(namespace, st); err != nil {
			errs = append(errs, err)
		}
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
		if ns, ok := TenantNamespaceFromUnit(u); ok && !seen[ns] {
			seen[ns] = true
			found = append(found, ns)
		}
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
