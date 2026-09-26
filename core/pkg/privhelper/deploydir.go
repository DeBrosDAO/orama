package privhelper

import (
	"fmt"
	"path/filepath"
	"regexp"

	"github.com/DeBrosOfficial/network/pkg/config"
	"github.com/DeBrosOfficial/network/pkg/constants"
)

// A deployment's directory, /opt/orama/.orama/data/deployments/<instance>, is
// what the orama-deploy-{node,npm,go,build}@ templates bind into the unit and
// run from (BindReadOnlyPaths, WorkingDirectory, the go runtime's ExecStart).
// PID 1 resolves those paths by name, following symlinks, and the directory is
// the orama user's. So a compromised orama process could replace it with a
// symlink — to another namespace's deployment, say — and the next start would
// hand that tree to this tenant's code.
//
// The helper checks the directory as root before it starts one of those
// units: a real directory, owned by the orama user, with no symlink in any
// component below /opt/orama (pkg/rootfs). The same check is ExecStartPre of
// the orama-deploy-{node,npm,go,build}@ templates (verify-deploy-dir), so a
// start that does not go through the helper — a reboot, a restart of the
// unit — is checked too. What neither closes is the orama user swapping the
// directory between the check and systemd reading it; that needs a directory
// the orama user cannot rename, which is the per-namespace deployment
// ownership work.

// DeploymentsAnchor is the root-owned directory the check walks down from.
const DeploymentsAnchor = config.ProductionBaseDir

// deploymentsDir is where the templates find a deployment's directory.
var deploymentsDir = constants.DeploymentsBaseDir(filepath.Join(config.ProductionBaseDir, ".orama"))

// dirBindingDeployUnit matches the templates that bind the deployment's
// directory; orama-deploy-clean@ binds none.
var dirBindingDeployUnit = regexp.MustCompile(`^orama-deploy-(node|npm|go|build)@(.+)\.service$`)

// deployInstanceName is the %i of those templates.
var deployInstanceName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,160}$`)

// DeploymentDirForInstance is the directory verify-deploy-dir checks for an
// orama-deploy-*@ instance name. Anything that is not that name — a path, a
// second argument — is refused before it is joined onto the deployments root.
func DeploymentDirForInstance(instance string) (string, error) {
	if !deployInstanceName.MatchString(instance) {
		return "", fmt.Errorf("deployment instance %q is not a name orama-deploy-*@ uses", instance)
	}
	return filepath.Join(deploymentsDir, instance), nil
}

// DeploymentDirToVerify is the deployment directory PID 1 is about to resolve
// for inv — a start or restart of a unit that binds one — or "" when it is none.
func DeploymentDirToVerify(inv Invocation) string {
	if inv.Tool != ToolSystemctl || len(inv.Args) != 2 || (inv.Args[0] != "start" && inv.Args[0] != "restart") {
		return ""
	}
	m := dirBindingDeployUnit.FindStringSubmatch(inv.Args[1])
	if m == nil {
		return ""
	}
	return filepath.Join(deploymentsDir, m[2])
}
