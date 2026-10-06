package lifecycle

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// namespaceRQLiteDataSubdir is where a tenant namespace keeps its rqlite data
// on a node that hosts one of its voters: data/namespaces/<ns>/rqlite/<node>.
const namespaceRQLiteDataSubdir = "rqlite"

// namespaceClusterStateFile is this node's record that it serves a namespace:
// what orama-node restores the namespace's services from at boot. A directory
// without it — data left behind by an unfinished teardown — runs nothing.
const namespaceClusterStateFile = "cluster-state.json"

// tenantRQLiteEndpoints returns the tenant rqlite instances on this node,
// read from wherever this node keeps its unit env files.
//
// That is unitEnvDir (unitenv.Dir) once it exists, and the old layout's
// namespacesDir/<ns>/rqlite.env before it does: the upgrade that crosses from
// the old layout runs this before orama-node has moved the files, and reading
// only the new tree there found no namespaces and silently skipped every
// tenant's leadership transfer. Both trees have the <dir>/<ns>/rqlite.env
// shape. A namespace this node serves (its cluster-state.json) with rqlite data
// here but no env in the tree read is an error: its leader cannot be handed
// over, and restarting it blind is what this step exists to prevent.
func tenantRQLiteEndpoints(unitEnvDir, namespacesDir string, index rqlite.Endpoint) (map[string]rqlite.Endpoint, map[string]error, error) {
	envDir, err := namespaceEnvTree(unitEnvDir, namespacesDir)
	if err != nil {
		return nil, nil, err
	}
	endpoints, failures, err := namespaceRQLiteEndpoints(envDir, index)
	if err != nil {
		return nil, nil, err
	}
	// The index has an env like a tenant but is handled on its own.
	delete(endpoints, indexNamespace)
	delete(failures, indexNamespace)

	missing, err := namespacesWithoutRQLiteEnv(namespacesDir, endpoints, failures)
	if err != nil {
		return nil, nil, err
	}
	if len(missing) > 0 {
		return nil, nil, fmt.Errorf("namespace(s) %s are served by this node (%s/<ns>/%s) with rqlite data here, but %s has no rqlite.env "+
			"for them, so their leadership cannot be handed over. If orama-node has not finished moving its env files, its log names "+
			"the path it stopped on; if the namespace no longer runs here, its directory is a leftover — see docs/COMMON_PROBLEMS.md",
			strings.Join(missing, ", "), namespacesDir, namespaceClusterStateFile, envDir)
	}
	return endpoints, failures, nil
}

// namespaceEnvTree is the env tree this node reads its units' env files from:
// unitEnvDir if it exists, else the old layout's.
func namespaceEnvTree(unitEnvDir, legacyDir string) (string, error) {
	info, err := os.Stat(unitEnvDir)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return legacyDir, nil
	case err != nil:
		return "", fmt.Errorf("inspect %s: %w", unitEnvDir, err)
	case !info.IsDir():
		return "", fmt.Errorf("%s is not a directory", unitEnvDir)
	default:
		return unitEnvDir, nil
	}
}

// namespacesWithoutRQLiteEnv lists the tenant namespaces this node serves
// with rqlite data here that were neither addressed nor reported as
// unaddressable.
func namespacesWithoutRQLiteEnv(namespacesDir string, endpoints map[string]rqlite.Endpoint, failures map[string]error) ([]string, error) {
	entries, err := os.ReadDir(namespacesDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", namespacesDir, err)
	}
	var missing []string
	for _, e := range entries {
		ns := e.Name()
		if !e.IsDir() || ns == indexNamespace {
			continue
		}
		if _, ok := endpoints[ns]; ok {
			continue
		}
		if _, ok := failures[ns]; ok {
			continue
		}
		served, err := pathExists(filepath.Join(namespacesDir, ns, namespaceClusterStateFile))
		if err != nil {
			return nil, err
		}
		hostsRQLite, err := pathExists(filepath.Join(namespacesDir, ns, namespaceRQLiteDataSubdir))
		if err != nil {
			return nil, err
		}
		if served && hostsRQLite {
			missing = append(missing, ns)
		}
	}
	sort.Strings(missing)
	return missing, nil
}

// pathExists reports whether path exists; an error other than "does not
// exist" is returned.
func pathExists(path string) (bool, error) {
	_, err := os.Stat(path)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, os.ErrNotExist):
		return false, nil
	default:
		return false, fmt.Errorf("inspect %s: %w", path, err)
	}
}
