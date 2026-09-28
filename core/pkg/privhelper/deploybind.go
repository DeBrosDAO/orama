package privhelper

import (
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

// DeployBindDropIn is the drop-in that lets a deployment bind port over TCP,
// and nothing else.
func DeployBindDropIn(port int) string {
	return "# Written by orama-privhelper (pkg/privhelper deploybind.go): the one port this deployment may bind.\n" +
		"[Service]\n" +
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
	path := DeployBindDropInPath(unitDir, runtime, instance)
	want := []byte(DeployBindDropIn(port))
	prev, err = root.ReadFile(path, rootfs.SmallFileLimit)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		prev = nil
	case err != nil:
		return nil, false, fmt.Errorf("read the bind drop-in of %s: %w", instance, err)
	case string(prev) == string(want):
		return prev, false, nil
	}
	if err := root.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, false, fmt.Errorf("create the drop-in directory of orama-deploy-%s@%s: %w", runtime, instance, err)
	}
	if err := root.WriteFile(path, want, 0o644); err != nil {
		return nil, false, fmt.Errorf("write the bind drop-in of orama-deploy-%s@%s: %w", runtime, instance, err)
	}
	return prev, true, nil
}

// RestoreDeployBind puts back what WriteDeployBind replaced: prev, or no file
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
	for _, runtime := range DeployBindRuntimes {
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
