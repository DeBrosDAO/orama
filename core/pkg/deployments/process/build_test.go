package process

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/deployments"
	"github.com/DeBrosOfficial/network/pkg/privhelper"
	"go.uber.org/zap"
)

// buildTemplate is the shipped orama-deploy-build@ template.
func buildTemplate(t *testing.T) string {
	t.Helper()
	path := filepath.Join(repoRoot(t), "systemd", "orama-deploy-"+buildRuntime+"@.service")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// The gateway starts the build unit as the orama user, through
// orama-privhelper. A build unit the helper refuses would fail every Node.js
// deploy on a node, with a permissions error.
func TestBuildUnitName_isAUnitOramaPrivhelperWillStart(t *testing.T) {
	unit := BuildUnitName("acme", "web")
	if unit != "orama-deploy-build@acme-web.service" {
		t.Fatalf("BuildUnitName = %q", unit)
	}
	if _, err := privhelper.Validate([]string{privhelper.ToolSystemctl, "start", unit}); err != nil {
		t.Fatalf("orama-privhelper refuses to start the build unit: %v", err)
	}
	// The longest instance a deployment can have, too.
	long := BuildUnitName(strings.Repeat("n", 64), strings.Repeat("a", MaxNameLength))
	if _, err := privhelper.Validate([]string{privhelper.ToolSystemctl, "start", long}); err != nil {
		t.Fatalf("orama-privhelper refuses the build unit of the longest deployment: %v", err)
	}
}

// On a node the install is the build unit, started and waited for — never npm
// run by this process.
func TestInstallDependencies_startsTheBuildUnit(t *testing.T) {
	var started []string
	m := &Manager{logger: zap.NewNop(), useSystemd: true, systemctl: startsOnly(func(unit string) error {
		started = append(started, unit)
		return nil
	})}
	if err := m.InstallDependencies(context.Background(), "acme", "web", registryApp(t)); err != nil {
		t.Fatalf("InstallDependencies: %v", err)
	}
	if len(started) != 1 || started[0] != "orama-deploy-build@acme-web.service" {
		t.Fatalf("started %v, want exactly the build unit", started)
	}
}

// A failed install fails the deploy, and says where npm's output is.
func TestInstallDependencies_reportsAFailedBuild(t *testing.T) {
	m := &Manager{logger: zap.NewNop(), useSystemd: true, systemctl: startsOnly(func(string) error {
		return errors.New("Job for orama-deploy-build@acme-web.service failed")
	})}
	err := m.InstallDependencies(context.Background(), "acme", "web", registryApp(t))
	if err == nil {
		t.Fatal("a failed build was reported as success")
	}
	if !strings.Contains(err.Error(), "journalctl -u orama-deploy-build@acme-web.service") {
		t.Errorf("the error does not say where npm's output is: %v", err)
	}
}

// The install runs no lifecycle script, and the template runs exactly the
// invocation this package documents.
func TestBuildTemplate_runsNpmWithScriptsRefused(t *testing.T) {
	unit := buildTemplate(t)
	exec := directiveValue(t, unit, "ExecStart=")
	if !strings.Contains(exec, "/usr/bin/npm "+strings.Join(npmInstallArgs, " ")) {
		t.Errorf("ExecStart does not run `npm %s`: %s", strings.Join(npmInstallArgs, " "), exec)
	}
	found := false
	for _, a := range npmInstallArgs {
		found = found || a == "--ignore-scripts"
	}
	if !found {
		t.Error("npm runs the tenant's and its dependencies' lifecycle scripts")
	}
}

// npm reads the project's .npmrc, which can name the binary it runs for git.
// It must run in a directory holding only the manifest and lockfile, not in
// the tenant's tree.
func TestBuildTemplate_runsNpmOutsideTheTenantsTree(t *testing.T) {
	exec := directiveValue(t, buildTemplate(t), "ExecStart=")
	if !strings.Contains(exec, `cd "$$out"`) {
		t.Errorf("npm runs in the deployment's own directory, where the tenant's .npmrc is: %s", exec)
	}
	for _, copied := range []string{"package.json", "package-lock.json", "npm-shrinkwrap.json"} {
		if !strings.Contains(exec, copied) {
			t.Errorf("%s is not copied to where npm runs", copied)
		}
	}
	if strings.Contains(exec, "cp -r") || strings.Contains(exec, "cp -a") {
		t.Error("the build copies the tenant's tree, .npmrc included")
	}
	if !strings.Contains(exec, "%C/orama-build/%i/") {
		t.Error("npm's output is not in the build unit's own cache directory")
	}
}

func TestBuildTemplate_isSandboxedLikeTheApp(t *testing.T) {
	unit := buildTemplate(t)
	for _, directive := range []string{
		"Type=oneshot",
		"DynamicUser=yes",
		"ProtectSystem=strict",
		"ProtectHome=yes",
		"NoNewPrivileges=yes",
		"PrivateDevices=yes",
		"PrivateTmp=yes",
		"ProtectKernelTunables=yes",
		"ProtectKernelModules=yes",
		"ProtectControlGroups=yes",
		"RestrictNamespaces=yes",
		"RestrictSUIDSGID=yes",
		"ProtectProc=invisible",
		"MemoryMax=",
		"TasksMax=",
		"CacheDirectory=orama-build/%i",
	} {
		if !strings.Contains(unit, directive) {
			t.Errorf("missing %s", directive)
		}
	}
	if strings.Contains(unit, "User=orama") || strings.Contains(unit, "User=root") {
		t.Error("the build runs as a named user; tenant code must never share the gateway's or root's")
	}
}

// Of /opt/orama the build sees only its own deployment: not the node's
// secrets, not another tenant's files.
func TestBuildTemplate_seesOnlyItsOwnDeployment(t *testing.T) {
	assertSeesOnlyItsOwnDeployment(t, buildTemplate(t))
}

// The build accepts no connections, so unlike the app it is denied loopback,
// where the node's control planes listen.
func TestBuildTemplate_isOffLoopbackAndTheOverlay(t *testing.T) {
	unit := buildTemplate(t)
	deny := directiveValue(t, unit, "IPAddressDeny=")
	for _, cidr := range []string{"localhost", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "169.254.0.0/16"} {
		if !strings.Contains(" "+deny+" ", " "+cidr+" ") {
			t.Errorf("%s is reachable from the build", cidr)
		}
	}
	// Allow wins over deny: any allowance reopens what the deny list closes.
	if strings.Contains(unit, "IPAddressAllow=") {
		t.Error("the build allows an address despite the deny list")
	}
}

// Loopback is denied, and with it every resolver the host's resolv.conf names
// (systemd-resolved's 127.0.0.53, CoreDNS's 127.0.0.1). The build resolves
// through its own file, which the installer writes root-owned; no '-' prefix,
// so a node without it refuses the build rather than failing every lookup.
func TestBuildTemplate_resolvesThroughItsOwnResolvConf(t *testing.T) {
	const want = "BindReadOnlyPaths=/etc/orama/build-resolv.conf:/etc/resolv.conf\n"
	if !strings.Contains(buildTemplate(t), want) {
		t.Errorf("the build resolves through the host's resolv.conf, whose resolvers are on denied loopback; want %s", want)
	}
}

// npm talks to the public registry only: a lockfile's tarball URLs are
// rewritten to it rather than followed.
func TestBuildTemplate_pinsTheRegistry(t *testing.T) {
	unit := buildTemplate(t)
	for _, env := range []string{
		"Environment=npm_config_registry=https://registry.npmjs.org/",
		"Environment=npm_config_replace_registry_host=always",
		"Environment=npm_config_allow_git=none",
		"Environment=npm_config_userconfig=/dev/null",
	} {
		if !strings.Contains(unit, env+"\n") {
			t.Errorf("missing %s", env)
		}
	}
}

// The runtime binds node_modules as PID 1, so the build only succeeds if npm
// left a directory there — and the check must be the last thing that runs,
// which an `exec npm` would skip.
func TestBuildTemplate_refusesANodeModulesThatIsNotADirectory(t *testing.T) {
	exec := directiveValue(t, buildTemplate(t), "ExecStart=")
	if strings.Contains(exec, "exec /usr/bin/npm") {
		t.Error("npm is exec'd, so nothing checks what it left behind")
	}
	if !strings.Contains(exec, "--no-fund || { rc=$$?; rm -rf node_modules; exit $$rc; }") {
		t.Error("a failed npm leaves its partial node_modules in place")
	}
	check := `if [ -L node_modules ] || { [ -e node_modules ] && [ ! -d node_modules ]; }; then`
	i, j := strings.Index(exec, "/usr/bin/npm "), strings.Index(exec, check)
	if j < 0 || j < i {
		t.Errorf("no check after npm that node_modules is a real directory: %s", exec)
	}
}

// orama-privhelper gives up on a command after five minutes. systemd has to
// end a hung install first, so the gateway hears npm failed rather than the
// helper abandoning the start.
func TestBuildTemplate_timesOutInsideThePrivhelperLimit(t *testing.T) {
	const privhelperCommandTimeout = 5 * time.Minute
	value := directiveValue(t, buildTemplate(t), "TimeoutStartSec=")
	d, err := time.ParseDuration(strings.TrimSpace(value) + "s")
	if err != nil {
		t.Fatalf("TimeoutStartSec=%s is not a number of seconds: %v", value, err)
	}
	if d <= 0 || d >= privhelperCommandTimeout {
		t.Errorf("TimeoutStartSec=%v must be positive and under orama-privhelper's %v", d, privhelperCommandTimeout)
	}
}

// The Node runtimes find what the build installed; the bind is from the build
// unit's cache directory, which the app cannot write — a bind source is
// resolved by PID 1, so an app-writable one could be a symlink to another
// tenant's files.
func TestNodeRuntimes_bindTheBuildOutputFromTheBuildUnit(t *testing.T) {
	want := "BindReadOnlyPaths=-%C/orama-build/%i/deps/node_modules:/opt/orama/.orama/data/deployments/node_modules"
	if !strings.Contains(directiveValue(t, buildTemplate(t), "ExecStart="), "%C/orama-build/%i/deps") {
		t.Fatal("the build does not write where the runtimes read")
	}
	for _, runtime := range []Runtime{RuntimeNode, RuntimeNPM} {
		unit := deployTemplate(t, runtime)
		if !strings.Contains(unit, want) {
			t.Errorf("%s does not bind the build output: want %s", runtime, want)
		}
		if strings.Contains(unit, "BindReadOnlyPaths=-%C/orama-deploy-%i") || strings.Contains(unit, "BindPaths=") {
			t.Errorf("%s binds from a directory the app itself can write", runtime)
		}
	}
}

// cleanTemplate is the shipped orama-deploy-clean@ template.
func cleanTemplate(t *testing.T) string {
	t.Helper()
	path := filepath.Join(repoRoot(t), "systemd", "orama-deploy-"+cleanRuntime+"@.service")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// perInstanceNames are the host-wide names a template derives from %i: its
// directories and its journal identifier.
var perInstanceNames = []string{"StateDirectory=", "CacheDirectory=", "SyslogIdentifier="}

// A deployment's runtime unit and another deployment's build unit must never
// name the same directory. The build's cache used to be
// orama-deploy-build-<instance>, which is exactly the runtime cache of a
// deployment in namespace "build-…": that tenant's app could write the
// node_modules another tenant's app then ran.
//
// A runtime name is prefix+<instance> and a build name prefix'+<instance'>;
// they can only be equal when one prefix starts the other, since an instance
// is never empty and never contains '/'.
func TestBuildNames_noRuntimeInstanceCanProduceThem(t *testing.T) {
	prefixes := func(unit string) []string {
		var out []string
		for _, line := range strings.Split(unit, "\n") {
			line = strings.TrimSpace(line)
			for _, d := range perInstanceNames {
				if value, ok := strings.CutPrefix(line, d); ok {
					prefix, _, found := strings.Cut(value, "%i")
					if !found {
						t.Fatalf("%s%s does not derive from the instance", d, value)
					}
					out = append(out, prefix)
				}
			}
		}
		return out
	}
	var runtime []string
	for _, rt := range []Runtime{RuntimeNode, RuntimeNPM, RuntimeGo} {
		runtime = append(runtime, prefixes(deployTemplate(t, rt))...)
	}
	for name, unit := range map[string]string{"build": buildTemplate(t), "clean": cleanTemplate(t)} {
		for _, b := range prefixes(unit) {
			for _, r := range runtime {
				if strings.HasPrefix(b, r) || strings.HasPrefix(r, b) {
					t.Errorf("%s names %q+<instance>, which a runtime's %q+<instance> can equal", name, b, r)
				}
			}
		}
	}
	// The premise: every instance a deployment can have is non-empty and
	// slash-free, which is what orama-privhelper and ValidateInstance hold.
	for _, pair := range [][2]string{{"acme", "web"}, {"build", "x"}, {"a.b", "c.d"}} {
		if err := ValidateInstance(pair[0], pair[1]); err != nil || strings.Contains(InstanceName(pair[0], pair[1]), "/") {
			t.Fatalf("instance of %v: %q (%v)", pair, InstanceName(pair[0], pair[1]), err)
		}
	}
	if ValidateInstance("a/b", "c") == nil {
		t.Fatal("an instance can contain '/', so a prefix check proves nothing")
	}
}

func TestCleanTemplate_emptiesTheBuildOutput(t *testing.T) {
	unit := cleanTemplate(t)
	for _, directive := range []string{"Type=oneshot", "DynamicUser=yes", "PrivateNetwork=yes", "CacheDirectory=orama-build/%i"} {
		if !strings.Contains(unit, directive) {
			t.Errorf("missing %s", directive)
		}
	}
	exec := directiveValue(t, unit, "ExecStart=")
	if !strings.Contains(exec, "%C/orama-build/%i/deps") {
		t.Errorf("ExecStart does not remove the build output: %s", exec)
	}
	if _, err := privhelper.Validate([]string{privhelper.ToolSystemctl, "start", CleanUnitName("acme", "web")}); err != nil {
		t.Fatalf("orama-privhelper refuses to start the clean unit: %v", err)
	}
}

// A dependency that is not a registry package never reaches the build unit.
func TestInstallDependencies_refusesAGitDependencyBeforeTheBuild(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "package.json", `{"dependencies":{"x":"github:evil/x"}}`)
	started := false
	m := &Manager{logger: zap.NewNop(), useSystemd: true, systemctl: startsOnly(func(string) error {
		started = true
		return nil
	})}
	if err := m.InstallDependencies(context.Background(), "acme", "web", dir); err == nil {
		t.Fatal("a git dependency was accepted")
	}
	if started {
		t.Fatal("the build unit was started for a refused manifest")
	}
}

// Removing a Node.js deployment removes what was installed for it; a Go
// deployment has nothing installed and costs no call.
func TestStop_clearsTheBuildOutputOfANodeDeployment(t *testing.T) {
	var started []string
	m := &Manager{logger: zap.NewNop(), useSystemd: true, systemctl: startsOnly(func(unit string) error {
		started = append(started, unit)
		return nil
	})}
	node := &deployments.Deployment{Namespace: "acme", Name: "web", Type: deployments.DeploymentTypeNodeJSBackend}
	if err := m.Stop(context.Background(), node); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if len(started) != 1 || started[0] != "orama-deploy-clean@acme-web.service" {
		t.Fatalf("started %v, want the clean unit", started)
	}

	started = nil
	goApp := &deployments.Deployment{Namespace: "acme", Name: "api", Type: deployments.DeploymentTypeGoBackend}
	if err := m.Stop(context.Background(), goApp); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if len(started) != 0 {
		t.Fatalf("started %v for a Go deployment", started)
	}
}

func writeFile(t *testing.T, dir, name, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

// registryApp is a deployment directory whose dependencies are all registry
// packages.
func registryApp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "package.json", `{"dependencies":{"express":"^4.19.2"}}`)
	return dir
}

// startsOnly is a systemctl seam that hands every `start` to start and lets
// every other verb succeed.
func startsOnly(start func(unit string) error) func(args ...string) error {
	return func(args ...string) error {
		if len(args) == 2 && args[0] == "start" {
			return start(args[1])
		}
		return nil
	}
}

// A deployment whose unit did not stop is still running on its dependencies;
// removing them from under it breaks it without stopping it.
func TestStop_keepsTheBuildOutputWhenTheUnitDidNotStop(t *testing.T) {
	var calls []string
	m := &Manager{logger: zap.NewNop(), useSystemd: true, systemctl: func(args ...string) error {
		calls = append(calls, strings.Join(args, " "))
		if args[0] == "stop" {
			return errors.New("Job for orama-deploy-node@acme-web.service canceled")
		}
		return nil
	}}
	node := &deployments.Deployment{Namespace: "acme", Name: "web", Type: deployments.DeploymentTypeNodeJSBackend}
	if err := m.Stop(context.Background(), node); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	for _, c := range calls {
		if strings.Contains(c, "orama-deploy-clean@") {
			t.Fatalf("the build output was cleared although the unit did not stop: %v", calls)
		}
	}
}
