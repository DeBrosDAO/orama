package process

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/deployments"
	"github.com/DeBrosOfficial/network/pkg/deploysecrets"
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

// MaxNameLength is the longest deployment name ValidateName accepts.
//
// A name ends up in three places with limits of their own. The subdomain is
// <name>-<6 random characters>, one DNS label, and a label is at most 63
// bytes: 63 - 7 = 56. The unit instance is <namespace>-<name>, which
// orama-privhelper accepts up to 161 characters, and a namespace is at most 64
// (httputil.ValidateNamespace): 64 + 1 + 56 fits. Tests hold both limits.
const MaxNameLength = 56

// namePattern is the character set orama-privhelper accepts in a deployment
// instance (pkg/privhelper deployUnit), so a name that passes here cannot be
// refused when the deployment starts.
var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

// ValidateName checks a deployment name where it enters the system.
//
// It used to be checked nowhere: any string was accepted, written to the
// registry and used for a directory, and the first thing to object was
// orama-privhelper refusing the unit at start — after the upload, the IPFS
// pin and the registry row.
func ValidateName(name string) error {
	if name == "" {
		return fmt.Errorf("deployment name is required")
	}
	if len(name) > MaxNameLength {
		return fmt.Errorf("deployment name %q is %d characters; the limit is %d", name, len(name), MaxNameLength)
	}
	if !namePattern.MatchString(name) {
		return fmt.Errorf("deployment name %q is not valid: use letters, digits, '-' and '_', starting with a letter or digit", name)
	}
	return nil
}

// ValidateInstance checks that an existing deployment's namespace and name map
// to an instance orama-privhelper will act on.
//
// It is for requests about a deployment that is already in the registry — an
// update, a replica — whose name may predate ValidateName: a dotted legacy
// name such as "my.app" maps to the valid instance "<ns>-my-app" and keeps
// working. A pair whose instance could never start, or would name a path
// outside the deployments directory, is refused.
func ValidateInstance(namespace, name string) error {
	if name == "" {
		return fmt.Errorf("deployment name is required")
	}
	if instance := InstanceName(namespace, name); !deploysecrets.ValidInstance(instance) {
		return fmt.Errorf("deployment %q in namespace %q maps to the unit instance %q, which is not a valid instance", name, namespace, instance)
	}
	return nil
}

// InstanceName is the systemd instance a deployment runs as, and the name of
// its directory. Dots are not allowed: systemd reads them as part of the unit
// suffix.
//
// The mapping is not injective — namespace "a" with name "b-c" and namespace
// "a-b" with name "c" are both "a-b-c" — and it cannot change without renaming
// every existing deployment's unit and directory. A new deployment is refused
// instead when its instance is already taken: the deployment service checks
// the registry, then claims the directory on the host before anything is
// written (pkg/gateway/handlers/deployments instance_claim.go).
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

// UnitPrefix is what every deployment unit starts with. It is what
// orama-privhelper's unit check and the namespace teardown glob are written
// against.
const UnitPrefix = "orama-deploy-"

// SystemctlVerbs is everything this package asks systemctl to do to a
// deployment unit, as an unprivileged user.
//
// It is a list rather than prose because orama-privhelper has to allow exactly
// it: a verb the gateway uses and the helper refuses fails at the moment a
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
