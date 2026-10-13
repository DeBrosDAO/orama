package systemd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// sqliteDir is the directory holding one directory of SQLite databases per
// namespace, beside the namespaces directory.
func (m *Manager) sqliteDir() string {
	if m.sqliteBase != "" {
		return m.sqliteBase
	}
	return filepath.Join(filepath.Dir(m.namespaceBase), "sqlite")
}

// validTenantDirName reports whether namespace is one path element that cannot
// point outside the directory it is joined to.
func validTenantDirName(namespace string) bool {
	return namespace != "" && namespace != "." && namespace != ".." && filepath.Base(namespace) == namespace
}

// RemoveTenantData deletes what a namespace keeps outside its own directory:
// its SQLite databases and the directories of the deployments it owns. A
// namespace created again under the same name would otherwise inherit them.
//
// It removes only <sqlite>/<namespace> and the deployment directories whose
// owner marker names this namespace (OwnedDeploymentInstances), each scoped
// under its base directory. Their units must already be torn down.
func (m *Manager) RemoveTenantData(namespace string) error {
	if !validTenantDirName(namespace) {
		return fmt.Errorf("refusing to remove the data of %q: it is not a single directory name", namespace)
	}
	var errs []error
	instances, err := m.OwnedDeploymentInstances(namespace)
	if err != nil {
		errs = append(errs, err)
	}
	for _, instance := range instances {
		if !validTenantDirName(instance) {
			errs = append(errs, fmt.Errorf("refusing to remove deployment directory %q: not a single directory name", instance))
			continue
		}
		dir := filepath.Join(m.deploymentsDir(), instance)
		if err := os.RemoveAll(dir); err != nil {
			errs = append(errs, fmt.Errorf("remove deployment directory %s: %w", dir, err))
		}
	}
	dbDir := filepath.Join(m.sqliteDir(), namespace)
	if err := os.RemoveAll(dbDir); err != nil {
		errs = append(errs, fmt.Errorf("remove the SQLite databases %s: %w", dbDir, err))
	}
	return errors.Join(errs...)
}
