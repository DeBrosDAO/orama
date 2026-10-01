package privhelper

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"github.com/DeBrosOfficial/network/pkg/deploysecrets"
	"github.com/DeBrosOfficial/network/pkg/rootfs"
)

// Which port a deployment may bind.
//
// The orama-deploy-{node,npm,go}@ templates set SocketBindDeny=any and allow
// nothing: they are shared by every deployment, and the port a deployment is
// given differs per instance. With TCP allowed outright, a tenant could bind
// any unprivileged port on 127.0.0.1 — the index gateway's 10104 while it
// restarted, which Caddy proxies public traffic (bearer tokens included) to,
// the chain RPC the node report reads, another tenant's port.
//
// The allow is a drop-in per instance, written by the helper as root:
//
//	/etc/systemd/system/orama-deploy-<runtime>@<instance>.service.d/orama-bind.conf
//	[Service]
//	SocketBindAllow=
//	SocketBindAllow=tcp:<port>
//
// It is not `systemctl set-property`. systemd 249-253 (Ubuntu 22.04, Debian 12)
// persist a set-property SocketBindAllow as "SocketBindAllow: tcp:<port>", a
// line their own parser ignores for want of '=', so the allow would vanish at
// the next daemon-reload or boot; and every release writes the whole list,
// template entries included, so it could never narrow what the template
// allows. systemd does not expand an environment variable in SocketBindAllow=
// either, so the port cannot come from the environment file.

// DeployPortMin and DeployPortMax bound the port a deployment may be allowed
// to bind: the deployment allocator's range (pkg/deployments UserMinPort and
// MaxPort; a test holds them together). Below it are the tenant and index
// namespace blocks, whose services a deployment must never stand in for.
const (
	DeployPortMin = 10200
	DeployPortMax = 19999
)

// DeployBindUnitDir is where the drop-ins go: root-owned, and read by systemd
// for every unit.
const DeployBindUnitDir = "/etc/systemd/system"

// DeployBindAnchor is the directory the helper resolves the drop-ins from
// without following a symlink (pkg/rootfs).
const DeployBindAnchor = "/etc"

// deployBindDropInName sorts after set-property's 50-*.conf drop-ins, and the
// empty SocketBindAllow= it starts with clears anything allowed before it.
const deployBindDropInName = "orama-bind.conf"

// deployBindPortOp is `deploy bind-port <instance> <runtime> <port>`.
const deployBindPortOp = "bind-port"

// DeployBindRuntimes are the templates that run a deployment and so bind its
// port: node, npm and go (pkg/deployments/process Runtime). The build and
// clean units accept no connections and bind nothing.
var DeployBindRuntimes = []string{"node", "npm", "go"}

// deployBuildUserOp is `deploy build-user <instance>`.
const deployBuildUserOp = "build-user"

// deployBuildRuntime is the template of the dependency install. It binds
// nothing, so it is not a DeployBindRuntimes entry, and its drop-in carries
// only the user.
const deployBuildRuntime = "build"

// deployCleanRuntime is the template that removes a build's output.
const deployCleanRuntime = "clean"

// deployUserHexLen is how much of the instance's SHA-256 names its dynamic
// users: 16 hex digits, 64 bits, so "orama-build-" or "orama-deploy-" plus them
// is 28 or 29 characters, inside the 31 a user name may have. A build has no
// port to name it by (the install runs before the deployment is saved and
// given one), and an instance ("<namespace>-<name>") is too long for a name.
// Two instances share a user only if their hashes collide: the odds across n
// deployments on a node are about n^2 / 2^65, and a collision costs the
// shared-uid situation of a template, not a break-out.
const deployUserHexLen = 16

// deployPortPattern is a port in plain decimal: no sign, no leading zero.
var deployPortPattern = regexp.MustCompile(`^[1-9][0-9]{0,4}$`)

// ValidDeployPort reports whether port may be allowed to a deployment.
func ValidDeployPort(port int) bool {
	return port >= DeployPortMin && port <= DeployPortMax
}

// isDeployBindRuntime reports whether runtime names a runtime template.
func isDeployBindRuntime(runtime string) bool {
	for _, r := range DeployBindRuntimes {
		if r == runtime {
			return true
		}
	}
	return false
}

// validateDeployBindPort checks `bind-port <instance> <runtime> <port>`.
func validateDeployBindPort(args []string) error {
	if len(args) != 4 {
		return fmt.Errorf("deploy %s takes an instance, a runtime and a port", deployBindPortOp)
	}
	if !deploysecrets.ValidInstance(args[1]) {
		return fmt.Errorf("deployment instance %q is not valid", args[1])
	}
	if !isDeployBindRuntime(args[2]) {
		return fmt.Errorf("deployment runtime %q is not one of %s", args[2], strings.Join(DeployBindRuntimes, ", "))
	}
	if _, err := ParseDeployPort(args[3]); err != nil {
		return err
	}
	return nil
}

// ParseDeployPort parses a port a deployment may be allowed to bind.
func ParseDeployPort(s string) (int, error) {
	if !deployPortPattern.MatchString(s) {
		return 0, fmt.Errorf("deployment port %q is not a port number", s)
	}
	port, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("deployment port %q: %w", s, err)
	}
	if !ValidDeployPort(port) {
		return 0, fmt.Errorf("deployment port %d is outside the deployment range %d-%d", port, DeployPortMin, DeployPortMax)
	}
	return port, nil
}

// DeployUserPrefix starts the name of the dynamic user a deployment runs as.
//
// systemd names a DynamicUser= after the unit, and for an instance of a
// template that is the template's prefix: every orama-deploy-go@ instance on a
// node was the one user "orama-deploy-go", one uid shared by every tenant's
// process, which ProtectProc=invisible does not hide from each other, so one
// tenant could read another's /proc/<pid>/environ and root, signal and ptrace
// it. A User= naming the dynamic user, in the drop-in of that instance alone,
// gives each deployment a user of its own. The name derives from the instance
// the helper is asked about and from nothing else the caller supplies: a name
// built from the port, which the gateway passes, would let a compromised
// gateway run one deployment as another's user by handing it that one's port.
const DeployUserPrefix = "orama-deploy-"

// deployBuildUserPrefix starts the name of the dynamic user of a build. It
// differs from DeployUserPrefix, so a build never runs as a deployment's user
// and the build and runtime caches of an instance are owned by different uids.
const deployBuildUserPrefix = "orama-build-"

// deployUserName is prefix plus the first deployUserHexLen hex digits of the
// SHA-256 of instance.
func deployUserName(prefix, instance string) string {
	sum := sha256.Sum256([]byte(instance))
	return prefix + hex.EncodeToString(sum[:])[:deployUserHexLen]
}

// DeployUserName is the dynamic user the runtime unit of instance runs as.
func DeployUserName(instance string) string {
	return deployUserName(DeployUserPrefix, instance)
}

// DeployBuildUserName is the dynamic user of the dependency install of
// instance. Concurrent builds of different tenants never share it, so none can
// read or signal another's npm.
func DeployBuildUserName(instance string) string {
	return deployUserName(deployBuildUserPrefix, instance)
}

// DeployBuildDropIn is the drop-in that gives the build of instance a user of
// its own.
func DeployBuildDropIn(instance string) string {
	return "# Written by orama-privhelper (pkg/privhelper deploybind.go): the user of this deployment's dependency install.\n" +
		"[Service]\n" +
		"User=" + DeployBuildUserName(instance) + "\n"
}

// DeployBindDropIn is the drop-in that lets instance bind port over TCP, and
// nothing else, and gives it a dynamic user of its own.
func DeployBindDropIn(instance string, port int) string {
	return "# Written by orama-privhelper (pkg/privhelper deploybind.go): the one port this deployment may bind, and its own user.\n" +
		"[Service]\n" +
		"User=" + DeployUserName(instance) + "\n" +
		"SocketBindAllow=\n" +
		"SocketBindAllow=tcp:" + strconv.Itoa(port) + "\n"
}

// deployBindUnitDropInDir is the drop-in directory of one runtime unit.
func deployBindUnitDropInDir(unitDir, runtime, instance string) string {
	return filepath.Join(unitDir, "orama-deploy-"+runtime+"@"+instance+".service.d")
}

// DeployBindDropInPath is where the drop-in of one runtime unit lives.
func DeployBindDropInPath(unitDir, runtime, instance string) string {
	return filepath.Join(deployBindUnitDropInDir(unitDir, runtime, instance), deployBindDropInName)
}

// WriteDeployBind writes the drop-in that lets orama-deploy-<runtime>@<instance>
// bind port, below root. It returns what the file held before (nil when there
// was none) and whether it changed; systemd reads it only after a
// daemon-reload, which the caller runs when it changed and undoes the write
// with RestoreDeployBind when the reload fails.
func WriteDeployBind(root rootfs.Root, unitDir, runtime, instance string, port int) (prev []byte, changed bool, err error) {
	if !isDeployBindRuntime(runtime) {
		return nil, false, fmt.Errorf("deployment runtime %q is not one of %s", runtime, strings.Join(DeployBindRuntimes, ", "))
	}
	if !deploysecrets.ValidInstance(instance) {
		return nil, false, fmt.Errorf("deployment instance %q is not valid", instance)
	}
	if !ValidDeployPort(port) {
		return nil, false, fmt.Errorf("deployment port %d is outside the deployment range %d-%d", port, DeployPortMin, DeployPortMax)
	}
	return writeDropIn(root, DeployBindDropInPath(unitDir, runtime, instance), []byte(DeployBindDropIn(instance, port)))
}

// deployBuildRuntimes are the oneshot templates that run as the build user:
// the install, and the removal of what it installed, which must own what the
// install wrote.
var deployBuildRuntimes = []string{deployBuildRuntime, deployCleanRuntime}

// WriteDeployBuildUser writes the drop-in that gives the build of instance, and
// the clean that removes its output, a user of their own. prev holds what each
// runtime's file held before (nil when there was none), for RestoreDeployBind;
// changed says whether any file changed, which needs a daemon-reload.
func WriteDeployBuildUser(root rootfs.Root, unitDir, instance string) (prev map[string][]byte, changed bool, err error) {
	if !deploysecrets.ValidInstance(instance) {
		return nil, false, fmt.Errorf("deployment instance %q is not valid", instance)
	}
	prev = map[string][]byte{}
	for _, runtime := range deployBuildRuntimes {
		p, c, err := writeDropIn(root, DeployBindDropInPath(unitDir, runtime, instance), []byte(DeployBuildDropIn(instance)))
		if err != nil {
			return prev, changed, err
		}
		prev[runtime] = p
		changed = changed || c
	}
	return prev, changed, nil
}

// writeDropIn writes want at path, below root, unless it is already there.
func writeDropIn(root rootfs.Root, path string, want []byte) (prev []byte, changed bool, err error) {
	prev, err = root.ReadFile(path, rootfs.SmallFileLimit)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		prev = nil
	case err != nil:
		return nil, false, fmt.Errorf("read %s: %w", path, err)
	case string(prev) == string(want):
		return prev, false, nil
	}
	if err := root.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, false, fmt.Errorf("create the drop-in directory %s: %w", filepath.Dir(path), err)
	}
	if err := root.WriteFile(path, want, 0o644); err != nil {
		return nil, false, fmt.Errorf("write %s: %w", path, err)
	}
	return prev, true, nil
}

// RestoreDeployBind puts back what WriteDeployBind or WriteDeployBuildUser replaced: prev, or no file
// when prev is nil.
func RestoreDeployBind(root rootfs.Root, unitDir, runtime, instance string, prev []byte) error {
	path := DeployBindDropInPath(unitDir, runtime, instance)
	if prev != nil {
		if err := root.WriteFile(path, prev, 0o644); err != nil {
			return fmt.Errorf("restore the bind drop-in of orama-deploy-%s@%s: %w", runtime, instance, err)
		}
		return nil
	}
	return removeDeployBind(root, unitDir, runtime, instance)
}

// ClearDeployBind removes the drop-ins of every runtime unit of instance. A
// missing one is not an error. systemd need not reload: the unit is stopped,
// and the next start of the instance writes its drop-in again and reloads.
func ClearDeployBind(root rootfs.Root, unitDir, instance string) error {
	if !deploysecrets.ValidInstance(instance) {
		return fmt.Errorf("deployment instance %q is not valid", instance)
	}
	for _, runtime := range append(append([]string{}, deployBuildRuntimes...), DeployBindRuntimes...) {
		if err := removeDeployBind(root, unitDir, runtime, instance); err != nil {
			return err
		}
	}
	return nil
}

// removeDeployBind removes one unit's drop-in, then its directory unless
// something else is in it.
func removeDeployBind(root rootfs.Root, unitDir, runtime, instance string) error {
	path := DeployBindDropInPath(unitDir, runtime, instance)
	if err := root.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove the bind drop-in of orama-deploy-%s@%s: %w", runtime, instance, err)
	}
	dir := filepath.Dir(path)
	err := root.Remove(dir)
	switch {
	case err == nil, errors.Is(err, fs.ErrNotExist):
		return nil
	case errors.Is(err, syscall.ENOTEMPTY), errors.Is(err, syscall.EEXIST):
		return nil // an operator's drop-in is there too; it stays
	default:
		return fmt.Errorf("remove %s: %w", dir, err)
	}
}

// AllowDeploymentPort lets orama-deploy-<runtime>@<instance> bind port, and no
// other, through the helper.
func AllowDeploymentPort(instance, runtime string, port int) error {
	cmd := Command(ToolDeploy, deployBindPortOp, instance, runtime, strconv.Itoa(port))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("deploy %s %s %s %d: %w: %s", deployBindPortOp, instance, runtime, port, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// AllowDeploymentBuildUser gives orama-deploy-build@<instance> a user of its
// own, through the helper.
func AllowDeploymentBuildUser(instance string) error {
	cmd := Command(ToolDeploy, deployBuildUserOp, instance)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("deploy %s %s: %w: %s", deployBuildUserOp, instance, err, strings.TrimSpace(string(out)))
	}
	return nil
}
