package install

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"

	"github.com/DeBrosOfficial/network/pkg/deployments"
	"github.com/DeBrosOfficial/network/pkg/deploysecrets"
	"github.com/DeBrosOfficial/network/pkg/privhelper"
	"github.com/DeBrosOfficial/network/pkg/rootfs"
)

// The orama-deploy-{node,npm,go}@ templates used to allow every TCP and UDP
// bind; they now allow none, and the gateway writes each deployment the one
// port it may bind when it starts it (pkg/privhelper deploybind.go). A
// deployment already running on a node was started before that: without a
// drop-in, the first restart after the upgrade — a crash, a reboot — would
// leave it unable to listen. The upgrade writes one for every deployment unit
// that is enabled, from the PORT its environment file already gives it, before
// the new templates are installed and systemd reloads.

// deployWantsDir is where `systemctl enable` links an enabled deployment unit
// (the templates' WantedBy=multi-user.target).
const deployWantsDir = "/etc/systemd/system/multi-user.target.wants"

// deployTemplateInstance is orama-deploy-<template>@<instance>.service; the
// template must be a runtime (privhelper.DeployBindRuntimes) and the instance
// valid (deploysecrets.ValidInstance), which runtimeUnit checks.
var deployTemplateInstance = regexp.MustCompile(`^orama-deploy-([a-z0-9]+)@(.+)\.service$`)

// runtimeUnit splits an enabled runtime unit's name into its runtime and
// instance; ok is false for anything else — the build and clean units, which
// bind nothing, the namespace services, a pre-template unit.
func runtimeUnit(name string) (runtime, instance string, ok bool) {
	m := deployTemplateInstance.FindStringSubmatch(name)
	if m == nil || !deploysecrets.ValidInstance(m[2]) {
		return "", "", false
	}
	for _, r := range privhelper.DeployBindRuntimes {
		if r == m[1] {
			return m[1], m[2], true
		}
	}
	return "", "", false
}

// deployBindMigration is where the migration reads and writes; a test points
// it at a temporary tree.
type deployBindMigration struct {
	unitRoot   rootfs.Root // anchor of unitDir
	unitDir    string      // where the drop-ins go
	wantsDir   string      // the enabled units
	secretRoot rootfs.Root // anchor of the environment files
	secretDir  string      // deploysecrets.Dir
}

// deployBindResult is what the migration did, for the operator.
type deployBindResult struct {
	allowed []string // "orama-deploy-node@acme-web.service may bind tcp:10200"
	skipped []string // a unit and why it was left without a port
}

// run writes the drop-in of every enabled deployment unit that has none, or
// an outdated one. A unit whose environment file is missing cannot start
// anyway (its EnvironmentFile= is required); one whose file names no port in
// the deployment range is left without a drop-in, so it cannot bind any port,
// and is reported. Anything that fails to read or write is an error.
func (m deployBindMigration) run() (deployBindResult, error) {
	var res deployBindResult
	entries, err := os.ReadDir(m.wantsDir)
	if errors.Is(err, fs.ErrNotExist) {
		return res, nil
	}
	if err != nil {
		return res, fmt.Errorf("list the enabled units in %s: %w", m.wantsDir, err)
	}
	for _, e := range entries {
		runtime, instance, ok := runtimeUnit(e.Name())
		if !ok {
			continue
		}
		unit := e.Name()
		port, why, err := m.deploymentPort(instance)
		if err != nil {
			return res, fmt.Errorf("%s: %w", unit, err)
		}
		if why != "" {
			res.skipped = append(res.skipped, unit+": "+why)
			continue
		}
		if _, _, err := privhelper.WriteDeployBind(m.unitRoot, m.unitDir, runtime, instance, port); err != nil {
			return res, err
		}
		res.allowed = append(res.allowed, fmt.Sprintf("%s may bind tcp:%d", unit, port))
	}
	return res, nil
}

// deploymentPort is the PORT instance's environment file gives it, or why
// there is none to allow.
func (m deployBindMigration) deploymentPort(instance string) (int, string, error) {
	path := deploysecrets.Path(m.secretDir, instance, deploysecrets.Env)
	data, err := m.secretRoot.ReadFile(path, privhelper.MaxDeploySecretBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, fmt.Sprintf("no environment file in %s, so the unit cannot start as it is; it binds no port until it is redeployed", m.secretDir), nil
	}
	if err != nil {
		return 0, "", fmt.Errorf("read the environment file: %w", err)
	}
	env, err := deployments.ParseEnvFile(string(data))
	if err != nil {
		return 0, fmt.Sprintf("its environment file cannot be read (%v); it binds no port until it is redeployed", err), nil
	}
	port, err := privhelper.ParseDeployPort(env["PORT"])
	if err != nil {
		return 0, fmt.Sprintf("%v; it binds no port until it is redeployed", err), nil
	}
	return port, "", nil
}

// installDeployBinds runs the migration on this node. The caller reloads
// systemd.
func (ps *ProductionSetup) installDeployBinds() error {
	res, err := deployBindMigration{
		unitRoot:   rootfs.At(privhelper.DeployBindAnchor),
		unitDir:    privhelper.DeployBindUnitDir,
		wantsDir:   deployWantsDir,
		secretRoot: rootfs.At(deploysecrets.Dir),
		secretDir:  deploysecrets.Dir,
	}.run()
	if err != nil {
		return fmt.Errorf("allow the running deployments their ports: %w", err)
	}
	for _, a := range res.allowed {
		ps.logf("  ✓ %s", a)
	}
	for _, s := range res.skipped {
		ps.logf("  ⚠️  deployment %s", s)
	}
	return nil
}
