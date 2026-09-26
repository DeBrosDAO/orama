package legacylayout

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/deployments"
	"github.com/DeBrosOfficial/network/pkg/deployments/process"
)

// Deployments on 0.122.x.
//
// A gateway extracted each deployment into <oramaDir>/deployments/<ns>/<name>
// and wrote it a unit of its own, /etc/systemd/system/orama-deploy-<ns>-<name>
// .service (dots in either name became hyphens), with the deployment's
// environment inline as Environment= lines and WorkingDirectory= the nested
// directory. Now a deployment runs as orama-deploy-<runtime>@<instance>, where
// the instance is process.InstanceName(ns, name), from the flat directory
// data/deployments/<instance>, which carries an owner marker naming (ns, name)
// — the host-level claim every gateway on the node checks before it writes
// there (pkg/gateway/handlers/deployments instance_claim.go).
//
// So each nested directory is moved to its flat place with the owner marker
// written first, and the environment the legacy unit ran with is staged where
// the template reads it. The legacy unit itself is root's to remove; the
// upgrade does that once this has run (see the upgrade's
// retireLegacyDeploymentUnits).

// SystemdUnitDir is where 0.122.x wrote each deployment's unit.
const SystemdUnitDir = "/etc/systemd/system"

// deployBaseDirMode is data/deployments' mode, as the gateway creates it: the
// units bind their own directory, and its owner marker must be readable.
const deployBaseDirMode = 0o755

// Owner marker, as pkg/gateway/handlers/deployments writes and reads it.
const (
	ownerMarkerName = ".orama-owner"
	ownerMarkerMode = 0o644
)

// ownerMarker is the marker's JSON shape.
type ownerMarker struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

// legacyDeployUnitPattern is a 0.122.x deployment unit: orama-deploy-<instance>
// .service with no template '@'. The instance is exactly what
// process.InstanceName makes of the unit's namespace and name, so the
// ambiguous split between them never has to be made.
var legacyDeployUnitPattern = regexp.MustCompile(`^` + regexp.QuoteMeta(process.UnitPrefix) + `([A-Za-z0-9][A-Za-z0-9_-]{0,160})\.service$`)

// LegacyDeploymentUnit returns the instance a legacy per-deployment unit file
// name belongs to, and false for any other name — the templates
// (orama-deploy-node@.service) and their instances included.
func LegacyDeploymentUnit(fileName string) (string, bool) {
	m := legacyDeployUnitPattern.FindStringSubmatch(fileName)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// deploymentMove moves one nested deployment directory to its flat place.
type deploymentMove struct {
	namespace, name string
	from, to        string
	instance        string
}

// planDeploymentDirs lists the deployment directories on the old layout. Every
// entry must be <ns>/<name>, both directories; anything else is refused, as is
// a flat directory that already exists and two deployments whose names map to
// the same instance.
func (m Migrator) planDeploymentDirs() ([]deploymentMove, error) {
	oldBase := filepath.Join(m.OramaDir, constants.DeploymentsSubdir)
	if ok, err := isDir(oldBase); err != nil || !ok {
		return nil, err
	}
	namespaces, err := os.ReadDir(oldBase)
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", oldBase, err)
	}
	newBase := constants.DeploymentsBaseDir(m.OramaDir)
	var moves []deploymentMove
	var problems []error
	byInstance := map[string]string{}
	for _, ns := range namespaces {
		nsDir := filepath.Join(oldBase, ns.Name())
		names, err := readDeploymentNamespace(nsDir, ns)
		if err != nil {
			problems = append(problems, err)
			continue
		}
		for _, name := range names {
			mv, err := planDeploymentMove(nsDir, newBase, ns.Name(), name, byInstance)
			if err != nil {
				problems = append(problems, err)
				continue
			}
			moves = append(moves, mv)
		}
	}
	return moves, errors.Join(problems...)
}

// readDeploymentNamespace lists the deployment names in one namespace
// directory of the old layout.
func readDeploymentNamespace(nsDir string, entry os.DirEntry) ([]string, error) {
	if entry.Type()&os.ModeSymlink != 0 || !entry.IsDir() {
		return nil, fmt.Errorf("%s is not a namespace directory the old layout wrote; move it aside and orama-node retries on its own", nsDir)
	}
	entries, err := os.ReadDir(nsDir)
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", nsDir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.Type()&os.ModeSymlink != 0 || !e.IsDir() {
			return nil, fmt.Errorf("%s is not a deployment directory the old layout wrote; move it aside and orama-node retries on its own",
				filepath.Join(nsDir, e.Name()))
		}
		names = append(names, e.Name())
	}
	return names, nil
}

// planDeploymentMove checks one deployment's move. byInstance records the
// directory each instance is planned from, to refuse two for one instance.
func planDeploymentMove(nsDir, newBase, namespace, name string, byInstance map[string]string) (deploymentMove, error) {
	from := filepath.Join(nsDir, name)
	if err := process.ValidateInstance(namespace, name); err != nil {
		return deploymentMove{}, fmt.Errorf("%s: %w", from, err)
	}
	instance := process.InstanceName(namespace, name)
	if other, taken := byInstance[instance]; taken {
		return deploymentMove{}, fmt.Errorf("%s and %s both map to the unit instance %s; "+
			"keep the one this node should run, move the other aside, and orama-node retries on its own", other, from, instance)
	}
	byInstance[instance] = from
	to := process.DeployDir(newBase, namespace, name)
	toExists, err := exists(to)
	if err != nil {
		return deploymentMove{}, err
	}
	if toExists {
		return deploymentMove{}, bothLayoutsError(from, to)
	}
	return deploymentMove{namespace: namespace, name: name, from: from, to: to, instance: instance}, nil
}

// applyDeploymentDirs marks and moves each planned directory, stages the
// environment its legacy unit ran with, and removes the emptied old tree.
func (m Migrator) applyDeploymentDirs(moves []deploymentMove) error {
	if len(moves) == 0 {
		return m.removeEmptyDeploymentTree()
	}
	if err := os.MkdirAll(constants.DeploymentsBaseDir(m.OramaDir), deployBaseDirMode); err != nil {
		return fmt.Errorf("create %s: %w", constants.DeploymentsBaseDir(m.OramaDir), err)
	}
	for _, mv := range moves {
		// Staged before the move: the move is what makes this the last run
		// for the deployment, so a helper that refuses leaves it to retry.
		if err := m.stageLegacyUnitEnv(mv.instance); err != nil {
			return err
		}
		if err := writeOwnerMarker(mv.from, mv.namespace, mv.name); err != nil {
			return err
		}
		if err := os.Rename(mv.from, mv.to); err != nil {
			return fmt.Errorf("move %s to %s: %w", mv.from, mv.to, err)
		}
		m.Logf("moved deployment %s/%s from %s to %s", mv.namespace, mv.name, mv.from, mv.to)
	}
	return m.removeEmptyDeploymentTree()
}

// writeOwnerMarker records (namespace, name) as the owner of dir, replacing
// any marker already there: an archive extracted by 0.122.x could carry one,
// and only this migration knows who the directory belongs to.
func writeOwnerMarker(dir, namespace, name string) error {
	data, err := json.Marshal(ownerMarker{Namespace: namespace, Name: name})
	if err != nil {
		return fmt.Errorf("encode the owner marker for %s: %w", dir, err)
	}
	return writeFileAtomic(filepath.Join(dir, ownerMarkerName), data, ownerMarkerMode)
}

// removeEmptyDeploymentTree removes <oramaDir>/deployments once every
// deployment has left it. Only empty directories are removed; planning
// refused anything else.
func (m Migrator) removeEmptyDeploymentTree() error {
	oldBase := filepath.Join(m.OramaDir, constants.DeploymentsSubdir)
	if ok, err := isDir(oldBase); err != nil || !ok {
		return err
	}
	namespaces, err := os.ReadDir(oldBase)
	if err != nil {
		return fmt.Errorf("list %s: %w", oldBase, err)
	}
	for _, ns := range namespaces {
		if err := os.Remove(filepath.Join(oldBase, ns.Name())); err != nil {
			return fmt.Errorf("remove the emptied %s: %w", filepath.Join(oldBase, ns.Name()), err)
		}
	}
	if err := os.Remove(oldBase); err != nil {
		return fmt.Errorf("remove the emptied %s: %w", oldBase, err)
	}
	return nil
}

// stageLegacyUnitEnv stages the environment instance's 0.122.x unit ran with,
// when that unit exists. It runs once per deployment, in the run that moves
// its directory: staged again later, it would overwrite the environment the
// gateway writes when it starts the deployment under the template. 0.122.x had
// no workload tokens, so there is none to stage; the gateway mints one when it
// starts the deployment.
func (m Migrator) stageLegacyUnitEnv(instance string) error {
	path := filepath.Join(m.systemdUnitDir(), process.UnitPrefix+instance+".service")
	data, _, err := readNoFollow(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read the legacy unit of deployment %s: %w", instance, err)
	}
	env, err := parseUnitEnvironment(string(data))
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	contents, err := deployments.RenderEnvFile(tenantEnv(env))
	if err != nil {
		return fmt.Errorf("render the environment in %s: %w", path, err)
	}
	if err := m.Stager.SetDeploymentEnv(instance, contents); err != nil {
		return fmt.Errorf("stage the environment of deployment %s through orama-privhelper: %w", instance, err)
	}
	m.Logf("staged the environment of deployment %s from %s", instance, path)
	return nil
}

func (m Migrator) systemdUnitDir() string {
	if m.SystemdUnitDir != "" {
		return m.SystemdUnitDir
	}
	return SystemdUnitDir
}

// legacyEntryPointKey is the tenant variable 0.122.x read the Node.js entry
// point from; the templates take it from the platform's ORAMA_ENTRYPOINT.
const legacyEntryPointKey = "ENTRY_POINT"

// tenantEnv is the legacy environment without the variables the platform owns
// (process.PlatformEnvKeys) and without ENTRY_POINT. 0.122.x wrote them inline
// beside the tenant's, from a tenant-writable deployment row, so a value found
// here is not the platform's to trust: staged, it would stand until the
// gateway rewrote the file, pointing the unit at a port, a namespace, a
// gateway or a script the platform did not choose. The gateway sets all of
// them when it starts the deployment.
func tenantEnv(env map[string]string) map[string]string {
	out := make(map[string]string, len(env))
	for key, value := range env {
		if key == legacyEntryPointKey || slices.Contains(process.PlatformEnvKeys, key) {
			continue
		}
		out[key] = value
	}
	return out
}

// parseUnitEnvironment reads the Environment="KEY=value" lines 0.122.x wrote,
// one variable per line.
func parseUnitEnvironment(unit string) (map[string]string, error) {
	env := map[string]string{}
	for _, raw := range strings.Split(unit, "\n") {
		line := strings.TrimSpace(raw)
		rest, ok := strings.CutPrefix(line, "Environment=")
		if !ok {
			continue
		}
		if len(rest) < 2 || rest[0] != '"' || rest[len(rest)-1] != '"' {
			return nil, fmt.Errorf("an Environment= line is not the quoted KEY=value 0.122.x wrote: %q", line)
		}
		key, value, found := strings.Cut(rest[1:len(rest)-1], "=")
		if !found || key == "" {
			return nil, fmt.Errorf("an Environment= line has no KEY=: %q", line)
		}
		env[key] = value
	}
	return env, nil
}

// isDir reports whether path is a directory, refusing a symlink or anything
// else in its place; a missing path is false.
func isDir(path string) (bool, error) {
	info, err := os.Lstat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("inspect %s: %w", path, err)
	case !info.IsDir():
		return false, fmt.Errorf("%s is not the directory the old layout wrote (mode %s); move it aside and orama-node retries on its own",
			path, info.Mode())
	default:
		return true, nil
	}
}
