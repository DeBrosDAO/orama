package process

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/deployments"
)

// Installing a Node.js deployment's dependencies.
//
// `npm install` used to run inside the gateway process, as the orama user,
// with the gateway's environment. npm runs lifecycle scripts by default — any
// dependency's postinstall, the tenant's own install scripts — and reads the
// project's .npmrc, which can name the binary npm runs for git (`git=`). Either
// ran the tenant's code with the gateway's access to the node's secrets and to
// orama-privhelper. The install now runs in orama-deploy-build@<instance>, a
// oneshot unit sandboxed like the app itself, with lifecycle scripts refused
// and only registry dependencies accepted (npmspec.go).
//
// Its output lives in the build unit's own cache directory, which the node and
// npm runtime units bind under the app. orama-deploy-clean@<instance> empties
// it, so a deployment never runs with dependencies an earlier holder of its
// instance installed.

// Templates, as orama-deploy-<runtime>@ names. orama-privhelper allows
// `systemctl start` on them because they have the shape of every deployment
// unit; a test holds that.
const (
	buildRuntime = "build"
	cleanRuntime = "clean"
)

// npmInstallArgs is the npm invocation that installs a deployment's
// dependencies, here and in the build template's ExecStart (a test holds the
// two together). --ignore-scripts refuses every lifecycle script — preinstall,
// install, postinstall, prepare — the package's own and its dependencies'.
var npmInstallArgs = []string{"install", "--omit=dev", "--ignore-scripts", "--no-audit", "--no-fund"}

// BuildUnitName is the oneshot unit that installs namespace/name's
// dependencies, e.g. orama-deploy-build@acme-web.service.
func BuildUnitName(namespace, name string) string {
	return fmt.Sprintf("orama-deploy-%s@%s.service", buildRuntime, InstanceName(namespace, name))
}

// CleanUnitName is the oneshot unit that removes namespace/name's installed
// dependencies, e.g. orama-deploy-clean@acme-web.service.
func CleanUnitName(namespace, name string) string {
	return fmt.Sprintf("orama-deploy-%s@%s.service", cleanRuntime, InstanceName(namespace, name))
}

// UsesBuildOutput reports whether a deployment of type t runs in a template
// that binds the build output: the node and npm runtimes.
func UsesBuildOutput(t deployments.DeploymentType) bool {
	return t == deployments.DeploymentTypeNodeJSBackend || t == deployments.DeploymentTypeNextJS
}

// InstallDependencies installs the npm dependencies of namespace/name, whose
// files are in workDir, after refusing any that are not registry packages.
//
// On a node that is `systemctl start` of its build unit, through
// orama-privhelper: a oneshot unit's start returns when npm has exited, with
// its status. Nothing is written into workDir.
//
// Without systemd (local development) npm runs directly in workDir, the way
// startDirect runs the app itself, with scripts still refused.
func (m *Manager) InstallDependencies(ctx context.Context, namespace, name, workDir string) error {
	if err := CheckRegistryOnlyDependencies(workDir); err != nil {
		return err
	}
	if !m.useSystemd {
		return installDirect(ctx, workDir)
	}
	unit := BuildUnitName(namespace, name)
	if err := m.runOneshot(unit); err != nil {
		return fmt.Errorf("npm install for %s failed (the node's journal has npm's output: journalctl -u %s): %w",
			InstanceName(namespace, name), unit, err)
	}
	return nil
}

// ClearDependencies removes whatever orama-deploy-build@ installed for
// namespace/name. A deployment that ships its own node_modules, or skips the
// install, must not find an earlier holder's under it; and a deleted one
// leaves nothing behind. Without systemd there is nothing separate to remove:
// the direct install writes into the deployment's own directory.
func (m *Manager) ClearDependencies(namespace, name string) error {
	if !m.useSystemd {
		return nil
	}
	unit := CleanUnitName(namespace, name)
	if err := m.runOneshot(unit); err != nil {
		return fmt.Errorf("remove the installed dependencies of %s: %w", InstanceName(namespace, name), err)
	}
	return nil
}

// runOneshot runs a oneshot unit to completion: `systemctl start` of one
// returns when its command has exited, with its status.
func (m *Manager) runOneshot(unit string) error {
	return m.runSystemctl("start", unit)
}

// installDirect runs npm in workDir, for a gateway without systemd.
func installDirect(ctx context.Context, workDir string) error {
	cmd := exec.CommandContext(ctx, "npm", npmInstallArgs...)
	cmd.Dir = workDir
	cmd.Env = append(cmd.Environ(), "NODE_ENV=production")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("npm %s in %s: %w: %s", strings.Join(npmInstallArgs, " "), workDir, err, strings.TrimSpace(string(out)))
	}
	return nil
}
