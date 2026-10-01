package privhelper

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/deploysecrets"
)

// What a deployment leaves on the node besides its code and secrets: the state
// and cache directories systemd creates for its DynamicUser unit
// (StateDirectory=/CacheDirectory= orama-deploy-%i, and the build's
// CacheDirectory=orama-build/%i). They live under /var/lib/private and
// /var/cache/private, which are root-only, with a symlink beside each in
// /var/lib and /var/cache. systemd removes none of them when the unit stops, so
// without `deploy purge` a deleted deployment's data stays on the node, owned
// by a uid a later deployment may be given.
const (
	deployPurge = "purge"      // purge <instance>: its state and cache directories
	deployState = "list-state" // list-state: the instances that have any
)

// deployStateDirs are the places an instance's directories are, relative to
// the root, with %s the instance. Each orama-deploy-<instance> directory has a
// symlink beside it, which is removed as itself. The build's cache is not
// listed twice: systemd symlinks only the first component of
// CacheDirectory=orama-build/<instance>, so /var/cache/orama-build is one
// symlink to private/orama-build for every instance, not a per-instance one,
// and a path through it is the directory already listed.
var deployStateDirs = []string{
	"var/lib/private/orama-deploy-%s",
	"var/lib/orama-deploy-%s",
	"var/cache/private/orama-deploy-%s",
	"var/cache/orama-deploy-%s",
	"var/cache/private/orama-build/%s",
}

// deployStateListings are the directories whose entries name instances, with
// the prefix an entry's name carries.
var deployStateListings = []struct{ dir, prefix string }{
	{"var/lib/private", "orama-deploy-"},
	{"var/cache/private", "orama-deploy-"},
	{"var/cache/private/orama-build", ""},
}

// PurgeDeployState removes the state and cache directories of instance below
// root ("/" on a node), unless one of its units is active or activating, which
// unitActive reports (the helper asks systemd; the gateway's word is not
// taken). The instance is validated, so it names one entry
// directly under each directory and nothing above it. os.RemoveAll unlinks
// what is below without following a symlink, and the directories above are
// root-owned, so the tenant cannot redirect the removal. A symlink beside a
// directory is removed as itself. A missing path is not an error.
func PurgeDeployState(root, instance string, unitActive func(unit string) (bool, error)) error {
	if !deploysecrets.ValidInstance(instance) {
		return fmt.Errorf("deployment instance %q is not valid", instance)
	}
	for _, runtime := range append(append([]string{}, DeployBindRuntimes...), deployBuildRuntimes...) {
		unit := DeployUnitName(runtime, instance)
		active, err := unitActive(unit)
		if err != nil {
			return fmt.Errorf("check whether %s is running before removing its directories: %w", unit, err)
		}
		if active {
			return fmt.Errorf("%s is running; its directories are not removed from under it", unit)
		}
	}
	var errs []error
	for _, pattern := range deployStateDirs {
		path := filepath.Join(root, fmt.Sprintf(pattern, instance))
		if err := os.RemoveAll(path); err != nil {
			errs = append(errs, fmt.Errorf("remove %s: %w", path, err))
		}
	}
	return errors.Join(errs...)
}

// ListDeployState lists, sorted, the instances that have a state or cache
// directory below root. An entry whose name is not a valid instance is left
// out: nothing here would purge it.
func ListDeployState(root string) ([]string, error) {
	found := map[string]bool{}
	for _, l := range deployStateListings {
		dir := filepath.Join(root, l.dir)
		entries, err := os.ReadDir(dir)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("list %s: %w", dir, err)
		}
		for _, e := range entries {
			instance, ok := strings.CutPrefix(e.Name(), l.prefix)
			if ok && instance != "" && deploysecrets.ValidInstance(instance) && !(l.prefix == "" && !e.IsDir()) {
				found[instance] = true
			}
		}
	}
	out := make([]string, 0, len(found))
	for instance := range found {
		out = append(out, instance)
	}
	sort.Strings(out)
	return out, nil
}

// DeployUnitName is the unit of runtime (node, npm, go, build or clean) that
// serves instance.
func DeployUnitName(runtime, instance string) string {
	return "orama-deploy-" + runtime + "@" + instance + ".service"
}

// PurgeDeployment removes instance's state and cache directories through the
// helper.
func PurgeDeployment(instance string) error {
	return runDeploy(deployPurge, instance, "")
}

// DeploymentStateInstances lists the instances with a state or cache directory
// on this node, through the helper.
func DeploymentStateInstances() ([]string, error) {
	out, err := Command(ToolDeploy, deployState).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("deploy %s: %w: %s", deployState, err, strings.TrimSpace(string(out)))
	}
	return strings.Fields(string(out)), nil
}
