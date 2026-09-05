package process

import (
	"fmt"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/deployments"
)

// Where a deployment lives, and what its unit is called.
//
// These used to be computed in six places from `filepath.Join(base, namespace,
// name)` and `fmt.Sprintf("orama-deploy-%s-%s", …)`. They are one place now
// because a systemd template can only derive its paths from its instance name,
// so the instance and the directory have to agree exactly — and "they agree
// because everybody spells it the same way" is what this file replaces.

// Runtime is which template unit runs a deployment.
type Runtime string

const (
	// RuntimeNode runs `node <entrypoint>`.
	RuntimeNode Runtime = "node"
	// RuntimeNPM runs `npm start`.
	RuntimeNPM Runtime = "npm"
	// RuntimeGo runs the compiled binary.
	RuntimeGo Runtime = "go"
)

// entryPointEnvKey names the script the node template runs. It is a variable
// because systemd expands one in an argument; the interpreter cannot be a
// variable, which is why there is a template per runtime.
const entryPointEnvKey = "ORAMA_ENTRYPOINT"

// npmStartEntryPoint is the ENTRY_POINT value a tenant sets to be run with
// `npm start` instead of node directly.
const npmStartEntryPoint = "npm:start"

// InstanceName is the systemd instance a deployment runs as, and the name of
// its directory. Dots are not allowed: systemd reads them as part of the unit
// suffix.
func InstanceName(namespace, name string) string {
	return sanitizeInstance(namespace) + "-" + sanitizeInstance(name)
}

func sanitizeInstance(s string) string {
	return strings.ReplaceAll(s, ".", "-")
}

// UnitName is the full systemd unit for a deployment, e.g.
// orama-deploy-node@acme-web.service.
func UnitName(runtime Runtime, namespace, name string) string {
	return fmt.Sprintf("orama-deploy-%s@%s.service", runtime, InstanceName(namespace, name))
}

// UnitPrefix is what every deployment unit starts with. It is what the sudoers
// grant and the namespace teardown glob are written against.
const UnitPrefix = "orama-deploy-"

// SystemctlVerbs is everything this package asks systemctl to do to a
// deployment unit, as an unprivileged user.
//
// It is a list rather than prose because the sudoers grant has to cover exactly
// it: a verb the gateway uses and the grant does not name fails at the moment a
// tenant deploys, on a node, with an error about permissions rather than about
// the deployment. A test holds the two together.
var SystemctlVerbs = []string{"enable", "disable", "start", "stop", "restart", "set-property"}

// DeployDir is where a deployment's files are extracted.
//
// It is flat — one directory per deployment, named by the instance — because
// the template derives it from %i, and %i cannot carry a slash.
func DeployDir(baseDeployPath, namespace, name string) string {
	return baseDeployPath + "/" + InstanceName(namespace, name)
}

// RuntimeFor is the template that runs a deployment, and the entry point the
// node template needs.
//
// A static deployment has no process — the gateway serves it from IPFS — so it
// has no runtime and asking for one is a programming error rather than a
// default.
func RuntimeFor(deployment *deployments.Deployment) (Runtime, string, error) {
	switch deployment.Type {
	case deployments.DeploymentTypeNextJS:
		// The CLI tarballs the standalone output directly, so server.js is at
		// the root of the deployment.
		return RuntimeNode, "server.js", nil
	case deployments.DeploymentTypeNodeJSBackend:
		entry := strings.TrimSpace(deployment.Environment["ENTRY_POINT"])
		switch {
		case entry == npmStartEntryPoint:
			return RuntimeNPM, "", nil
		case entry == "":
			return RuntimeNode, "index.js", nil
		default:
			return RuntimeNode, entry, nil
		}
	case deployments.DeploymentTypeGoBackend:
		return RuntimeGo, "", nil
	default:
		return "", "", fmt.Errorf("deployment type %q has no runtime: it is served rather than run", deployment.Type)
	}
}
