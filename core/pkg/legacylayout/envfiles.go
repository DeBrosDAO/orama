package legacylayout

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/deploysecrets"
	"github.com/DeBrosOfficial/network/pkg/unitenv"
)

// envReadingServices are the services whose units are run today and read an
// env file (EnvironmentFile=/var/lib/orama-unit-env/%i/<svc>.env in
// core/systemd/orama-namespace-<svc>@.service; a test keeps the two in step).
// Their old env files are staged.
var envReadingServices = map[string]bool{
	"rqlite": true, "olric": true, "gateway": true, "sfu": true, "pubsub": true,
	"ipfs": true, "ipfs-cluster": true, "ipfs-gc": true, "vault": true, "caddy": true,
	"sni-router": true, "coredns": true,
}

// obsoleteEnvServices are services the old layout wrote an env file for that
// must not get one now. Their old files are deleted, never staged:
//
//   - wireguard runs as root, tor and ntfy as their own users: an env file the
//     orama user wrote would set variables for a process it does not own, and
//     orama-privhelper refuses to store one;
//   - anyone-client was replaced by Tor;
//   - turn is the per-namespace TURN server the shared orama-turn.service
//     replaced (bugboard #283). Its template remains only so old instances can
//     be stopped; an env file in the unit env tree is what makes the node
//     start and enable orama-namespace-turn@<ns> (systemd.Manager,
//     GetProductionServices), which would contend for 3478 with the shared
//     server.
var obsoleteEnvServices = map[string]bool{"wireguard": true, "tor": true, "ntfy": true, "anyone-client": true, "turn": true}

// unitEnvFile is one data/namespaces/<ns>/<svc>.env of the old layout.
type unitEnvFile struct {
	namespace, service, path string
	contents                 []byte
	// staged: the unit env tree already holds these contents (an earlier run
	// staged it and stopped before deleting this file).
	staged bool
	// obsolete: its unit reads no env file any more; it is only deleted.
	obsolete bool
}

// planUnitEnvs reads every old-layout namespace env file and decides what each
// needs. Names are checked the way orama-privhelper checks them, so a file the
// helper would refuse fails here, before anything is changed.
func (m Migrator) planUnitEnvs() ([]unitEnvFile, error) {
	root := NamespaceEnvDir(m.OramaDir)
	namespaces, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", root, err)
	}
	var files []unitEnvFile
	var conflicts []error
	for _, ns := range namespaces {
		if !ns.IsDir() {
			continue
		}
		nsFiles, err := m.planNamespaceEnvs(root, ns.Name())
		if err != nil {
			conflicts = append(conflicts, err)
			continue
		}
		files = append(files, nsFiles...)
	}
	return files, errors.Join(conflicts...)
}

// planNamespaceEnvs is planUnitEnvs for one namespace directory.
func (m Migrator) planNamespaceEnvs(root, namespace string) ([]unitEnvFile, error) {
	dir := filepath.Join(root, namespace)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", dir, err)
	}
	var files []unitEnvFile
	for _, e := range entries {
		if e.IsDir() || !isEnvFileName(e.Name()) {
			continue
		}
		f := unitEnvFile{namespace: namespace, service: strings.TrimSuffix(e.Name(), envFileSuffix), path: filepath.Join(dir, e.Name())}
		if obsoleteEnvServices[f.service] {
			f.obsolete = true
			files = append(files, f)
			continue
		}
		if !envReadingServices[f.service] || !unitenv.Valid(f.namespace, f.service) {
			return nil, fmt.Errorf("%s is not the env file of a namespace unit that reads one (namespace %q, service %q); "+
				"move it out of %s and orama-node retries on its own", f.path, f.namespace, f.service, dir)
		}
		if f.contents, _, err = readNoFollow(f.path); err != nil {
			return nil, err
		}
		if f.staged, err = m.alreadyStaged(f); err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return files, nil
}

// alreadyStaged compares f with the unit env tree's copy. Absent → not staged;
// identical → staged; different → both layouts hold an env for the unit.
func (m Migrator) alreadyStaged(f unitEnvFile) (bool, error) {
	target := unitenv.Path(m.UnitEnvDir, f.namespace, f.service)
	current, _, err := readNoFollow(target)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("compare %s with %s: %w", f.path, target, err)
	case bytes.Equal(current, f.contents):
		return true, nil
	default:
		return false, bothLayoutsError(f.path, target)
	}
}

func (m Migrator) applyUnitEnvs(files []unitEnvFile) error {
	for _, f := range files {
		if f.obsolete {
			if err := os.Remove(f.path); err != nil {
				return fmt.Errorf("remove %s, whose unit reads no env file any more: %w", f.path, err)
			}
			m.Logf("removed %s: orama-namespace-%s@%s reads no env file any more", f.path, f.service, f.namespace)
			continue
		}
		if !f.staged {
			if err := m.Stager.SetUnitEnv(f.namespace, f.service, string(f.contents)); err != nil {
				return fmt.Errorf("stage %s into %s: %w", f.path, m.UnitEnvDir, err)
			}
		}
		if err := os.Remove(f.path); err != nil {
			return fmt.Errorf("remove %s after staging it: %w", f.path, err)
		}
		m.Logf("staged %s as the env of orama-namespace-%s@%s", f.path, f.service, f.namespace)
	}
	return nil
}

// Deployment file names: orama-deploy-<instance>.env and .token, as the
// orama-deploy-*@ templates name them (deploysecrets.Path).
const deploymentFilePrefix = "orama-deploy-"

var deploymentKinds = []deploysecrets.Kind{deploysecrets.Env, deploysecrets.Token}

// deploymentFile is one file of the old deployment-env directory.
type deploymentFile struct {
	instance, path string
	kind           deploysecrets.Kind
	contents       []byte
}

// planDeploymentFiles reads the old deployment-env directory. Unlike the unit
// env tree, the directory these files move to is root-only (0700), so this
// process cannot see what it holds: every file here is staged. Nothing else
// writes there before this runs — gateways start after it — so what is staged
// is what the deployment last ran with.
func (m Migrator) planDeploymentFiles() ([]deploymentFile, error) {
	dir := DeploymentEnvDir(m.OramaDir)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", dir, err)
	}
	var files []deploymentFile
	for _, e := range entries {
		f, err := parseDeploymentFile(dir, e.Name())
		if err != nil {
			return nil, err
		}
		if f.contents, _, err = readNoFollow(f.path); err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return files, nil
}

// parseDeploymentFile names the deployment and kind of dir/name, refusing a
// name orama-privhelper would refuse.
func parseDeploymentFile(dir, name string) (deploymentFile, error) {
	path := filepath.Join(dir, name)
	for _, kind := range deploymentKinds {
		suffix := "." + string(kind)
		if !strings.HasPrefix(name, deploymentFilePrefix) || !strings.HasSuffix(name, suffix) {
			continue
		}
		instance := strings.TrimSuffix(strings.TrimPrefix(name, deploymentFilePrefix), suffix)
		if deploysecrets.ValidInstance(instance) {
			return deploymentFile{instance: instance, path: path, kind: kind}, nil
		}
	}
	return deploymentFile{}, fmt.Errorf("%s is not a deployment environment or token file (%s<instance>.env|.token); "+
		"move it out of %s and orama-node retries on its own", path, deploymentFilePrefix, dir)
}

func (m Migrator) applyDeploymentFiles(files []deploymentFile) error {
	for _, f := range files {
		var err error
		if f.kind == deploysecrets.Env {
			err = m.Stager.SetDeploymentEnv(f.instance, string(f.contents))
		} else {
			err = m.Stager.SetDeploymentToken(f.instance, string(f.contents))
		}
		if err != nil {
			return fmt.Errorf("stage %s: %w", f.path, err)
		}
		if err := os.Remove(f.path); err != nil {
			return fmt.Errorf("remove %s after staging it: %w", f.path, err)
		}
		m.Logf("staged %s for orama-deploy-*@%s", f.path, f.instance)
	}
	// Planning refused anything but staged files, so the directory is empty.
	dir := DeploymentEnvDir(m.OramaDir)
	if err := os.Remove(dir); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove %s once every file in it was staged: %w", dir, err)
	}
	return nil
}
