package privhelper

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/rootfs"
)

func TestValidate_AllowsBindPortForEachRuntime(t *testing.T) {
	for _, argv := range [][]string{
		{"deploy", "bind-port", "acme-web", "node", "10200"},
		{"deploy", "bind-port", "my_ns-api-v2", "npm", "15000"},
		{"deploy", "bind-port", "acme-web", "go", "19999"},
	} {
		if _, err := Validate(argv); err != nil {
			t.Errorf("%q refused: %v", argv, err)
		}
	}
}

// What a compromised gateway would ask for: a platform port, another
// template's unit, a path, a malformed number.
func TestValidate_RefusesAnyOtherBindPort(t *testing.T) {
	for _, argv := range [][]string{
		{"deploy", "bind-port"},
		{"deploy", "bind-port", "acme-web"},
		{"deploy", "bind-port", "acme-web", "node"},
		{"deploy", "bind-port", "acme-web", "node", "10200", "10201"},
		{"deploy", "bind-port", "acme-web", "node", "10104"}, // the index gateway
		{"deploy", "bind-port", "acme-web", "node", "31001"}, // the chain RPC
		{"deploy", "bind-port", "acme-web", "node", "10199"},
		{"deploy", "bind-port", "acme-web", "node", "20000"},
		{"deploy", "bind-port", "acme-web", "node", "0"},
		{"deploy", "bind-port", "acme-web", "node", "010200"},
		{"deploy", "bind-port", "acme-web", "node", "+10200"},
		{"deploy", "bind-port", "acme-web", "node", "10200-10300"},
		{"deploy", "bind-port", "acme-web", "node", "tcp:10200"},
		{"deploy", "bind-port", "acme-web", "node", ""},
		{"deploy", "bind-port", "acme-web", "build", "10200"},
		{"deploy", "bind-port", "acme-web", "clean", "10200"},
		{"deploy", "bind-port", "acme-web", "", "10200"},
		{"deploy", "bind-port", "../etc", "node", "10200"},
		{"deploy", "bind-port", "acme/web", "node", "10200"},
		{"deploy", "bind-port", ".acme", "node", "10200"},
		{"deploy", "bind-port", "", "node", "10200"},
	} {
		if _, err := Validate(argv); err == nil {
			t.Errorf("%q allowed", argv)
		}
	}
}

func TestBindPort_NeedsNoInput(t *testing.T) {
	inv, err := Validate([]string{"deploy", "bind-port", "acme-web", "node", "10200"})
	if err != nil {
		t.Fatal(err)
	}
	if inv.NeedsInput() {
		t.Error("bind-port reads stdin; it takes everything in its arguments")
	}
}

// The drop-in systemd reads: the empty assignment clears any allow before it,
// then the one TCP port.
func TestDeployBindDropIn_AllowsExactlyTheTCPPort(t *testing.T) {
	var allows []string
	for _, line := range strings.Split(DeployBindDropIn("acme-web", 10234), "\n") {
		if v, ok := strings.CutPrefix(line, "SocketBindAllow="); ok {
			allows = append(allows, v)
		}
	}
	if len(allows) != 2 || allows[0] != "" || allows[1] != "tcp:10234" {
		t.Fatalf("allows %q, want a reset then tcp:10234", allows)
	}
	if !strings.Contains(DeployBindDropIn("acme-web", 10234), "[Service]\n") {
		t.Error("no [Service] section: systemd would ignore the setting")
	}
}

func TestDeployBindDropInPath_IsTheInstancesOwnDropInDirectory(t *testing.T) {
	got := DeployBindDropInPath("/etc/systemd/system", "npm", "acme-web")
	if got != "/etc/systemd/system/orama-deploy-npm@acme-web.service.d/orama-bind.conf" {
		t.Errorf("got %s", got)
	}
}

func bindTree(t *testing.T) (rootfs.Root, string) {
	t.Helper()
	anchor := t.TempDir()
	unitDir := filepath.Join(anchor, "systemd", "system")
	if err := os.MkdirAll(unitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	return rootfs.At(anchor), unitDir
}

func TestWriteDeployBind_WritesReportsAChangeAndIsIdempotent(t *testing.T) {
	root, unitDir := bindTree(t)
	prev, changed, err := WriteDeployBind(root, unitDir, "node", "acme-web", 10200)
	if err != nil || !changed || prev != nil {
		t.Fatalf("first write: prev %q, changed %v, err %v", prev, changed, err)
	}
	data, err := os.ReadFile(DeployBindDropInPath(unitDir, "node", "acme-web"))
	if err != nil || string(data) != DeployBindDropIn("acme-web", 10200) {
		t.Fatalf("drop-in %q, %v", data, err)
	}

	// The same port again: nothing to reload.
	if _, changed, err := WriteDeployBind(root, unitDir, "node", "acme-web", 10200); err != nil || changed {
		t.Fatalf("rewrite of the same port: changed %v, err %v", changed, err)
	}

	// A new port replaces the old one; the old one is not kept.
	prev, changed, err = WriteDeployBind(root, unitDir, "node", "acme-web", 10201)
	if err != nil || !changed || string(prev) != DeployBindDropIn("acme-web", 10200) {
		t.Fatalf("new port: prev %q, changed %v, err %v", prev, changed, err)
	}
	data, _ = os.ReadFile(DeployBindDropInPath(unitDir, "node", "acme-web"))
	if strings.Contains(string(data), "10200") {
		t.Errorf("the old port is still allowed:\n%s", data)
	}
}

func TestWriteDeployBind_RefusesWhatValidateRefuses(t *testing.T) {
	root, unitDir := bindTree(t)
	for _, tc := range []struct {
		runtime, instance string
		port              int
	}{
		{"build", "acme-web", 10200},
		{"node", "../x", 10200},
		{"node", "acme-web", 10104},
		{"node", "acme-web", 20000},
	} {
		if _, _, err := WriteDeployBind(root, unitDir, tc.runtime, tc.instance, tc.port); err == nil {
			t.Errorf("%+v written", tc)
		}
	}
	entries, _ := os.ReadDir(unitDir)
	if len(entries) != 0 {
		t.Errorf("a refused write left %v behind", entries)
	}
}

// A symlinked drop-in directory is refused, not followed: root would write
// wherever it points.
func TestWriteDeployBind_RefusesASymlinkedDropInDirectory(t *testing.T) {
	root, unitDir := bindTree(t)
	elsewhere := t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(unitDir, "orama-deploy-node@acme-web.service.d")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := WriteDeployBind(root, unitDir, "node", "acme-web", 10200); err == nil {
		t.Fatal("wrote through a symlink")
	}
	if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
		t.Errorf("wrote %v through the symlink", entries)
	}
}

func TestRestoreDeployBind_PutsBackWhatWasThere(t *testing.T) {
	root, unitDir := bindTree(t)
	path := DeployBindDropInPath(unitDir, "go", "acme-api")

	// Nothing before: the restore removes the file and its directory.
	prev, _, err := WriteDeployBind(root, unitDir, "go", "acme-api", 10200)
	if err != nil {
		t.Fatal(err)
	}
	if err := RestoreDeployBind(root, unitDir, "go", "acme-api", prev); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Errorf("the drop-in directory is still there: %v", err)
	}

	// A previous port: the restore writes it back.
	if _, _, err := WriteDeployBind(root, unitDir, "go", "acme-api", 10200); err != nil {
		t.Fatal(err)
	}
	prev, _, err = WriteDeployBind(root, unitDir, "go", "acme-api", 10300)
	if err != nil {
		t.Fatal(err)
	}
	if err := RestoreDeployBind(root, unitDir, "go", "acme-api", prev); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); string(data) != DeployBindDropIn("acme-api", 10200) {
		t.Errorf("restored %q", data)
	}
}

func TestClearDeployBind_RemovesEveryRuntimesDropIn(t *testing.T) {
	root, unitDir := bindTree(t)
	for _, runtime := range DeployBindRuntimes {
		if _, _, err := WriteDeployBind(root, unitDir, runtime, "acme-web", 10200); err != nil {
			t.Fatal(err)
		}
	}
	// Another instance's drop-in is not touched.
	if _, _, err := WriteDeployBind(root, unitDir, "node", "acme-web2", 10201); err != nil {
		t.Fatal(err)
	}
	if err := ClearDeployBind(root, unitDir, "acme-web"); err != nil {
		t.Fatalf("clear: %v", err)
	}
	for _, runtime := range DeployBindRuntimes {
		if _, err := os.Stat(DeployBindDropInPath(unitDir, runtime, "acme-web")); !os.IsNotExist(err) {
			t.Errorf("%s drop-in still there: %v", runtime, err)
		}
	}
	if _, err := os.Stat(DeployBindDropInPath(unitDir, "node", "acme-web2")); err != nil {
		t.Errorf("another instance's drop-in went too: %v", err)
	}
	// Nothing left to clear is not an error.
	if err := ClearDeployBind(root, unitDir, "acme-web"); err != nil {
		t.Errorf("a second clear: %v", err)
	}
	if err := ClearDeployBind(root, unitDir, "../etc"); err == nil {
		t.Error("cleared an instance that is a path")
	}
}

// An operator's own drop-in beside ours stays, and so does its directory.
func TestClearDeployBind_KeepsAnOperatorsDropIn(t *testing.T) {
	root, unitDir := bindTree(t)
	if _, _, err := WriteDeployBind(root, unitDir, "node", "acme-web", 10200); err != nil {
		t.Fatal(err)
	}
	theirs := filepath.Join(filepath.Dir(DeployBindDropInPath(unitDir, "node", "acme-web")), "10-operator.conf")
	if err := os.WriteFile(theirs, []byte("[Service]\nNice=5\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ClearDeployBind(root, unitDir, "acme-web"); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if _, err := os.Stat(theirs); err != nil {
		t.Errorf("the operator's drop-in went: %v", err)
	}
	if _, err := os.Stat(DeployBindDropInPath(unitDir, "node", "acme-web")); !os.IsNotExist(err) {
		t.Errorf("ours is still there: %v", err)
	}
}

// systemd names a DynamicUser after the template, so without a User= every
// deployment of a runtime was one user and one uid. Each instance is one user,
// derived from the instance alone: the port the caller names plays no part, so
// a caller cannot run an instance as another's user by naming that one's port.
func TestDeployBindDropIn_GivesEachDeploymentItsOwnUser(t *testing.T) {
	userOf := func(instance string, port int) string {
		for _, line := range strings.Split(DeployBindDropIn(instance, port), "\n") {
			if v, ok := strings.CutPrefix(line, "User="); ok {
				return v
			}
		}
		return ""
	}
	a, b := userOf("acme-web", 10209), userOf("acme-api", 10210)
	if a == "" || b == "" || a == b {
		t.Fatalf("users %q and %q, want one distinct user per instance", a, b)
	}
	if a != DeployUserName("acme-web") {
		t.Errorf("user %q, want %q", a, DeployUserName("acme-web"))
	}
	if again := userOf("acme-web", 10299); again != a {
		t.Errorf("the user changed with the port: %q then %q", a, again)
	}
	longest := strings.Repeat("n", 64) + "-" + strings.Repeat("a", 64)
	if n := len(DeployUserName(longest)); n > 31 {
		t.Errorf("a user name of %d characters is longer than systemd accepts", n)
	}
	if DeployUserName("acme-web") == DeployBuildUserName("acme-web") {
		t.Error("an instance's runtime and build users are the same")
	}
}

// A drop-in written before the user was named is rewritten, so an upgrade
// reaches the deployments already on a node.
func TestWriteDeployBind_RewritesADropInWithoutAUser(t *testing.T) {
	root, unitDir := bindTree(t)
	path := DeployBindDropInPath(unitDir, "go", "acme-api")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	old := "[Service]\nSocketBindAllow=\nSocketBindAllow=tcp:10200\n"
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	prev, changed, err := WriteDeployBind(root, unitDir, "go", "acme-api", 10200)
	if err != nil || !changed || string(prev) != old {
		t.Fatalf("prev %q changed %v err %v, want the old drop-in replaced", prev, changed, err)
	}
	if data, _ := os.ReadFile(path); !strings.Contains(string(data), "User="+DeployUserName("acme-api")) {
		t.Errorf("drop-in %q names no user", data)
	}
}

func TestDeployBuildUserName_isDistinctPerInstanceAndFitsAUserName(t *testing.T) {
	longest := strings.Repeat("n", 64) + "-" + strings.Repeat("a", 64)
	seen := map[string]string{}
	for _, instance := range []string{"acme-web", "acme-web2", "acme2-web", longest, "a"} {
		name := DeployBuildUserName(instance)
		if len(name) > 31 {
			t.Errorf("%q is %d characters, more than a user name may have", name, len(name))
		}
		if other, dup := seen[name]; dup {
			t.Errorf("%q and %q share the build user %q", instance, other, name)
		}
		seen[name] = instance
	}
	first, second := DeployBuildUserName("acme-web"), DeployBuildUserName("acme-web")
	if first != second {
		t.Error("the user of one instance changes between calls")
	}
}

func TestWriteDeployBuildUser_writesTheUserOnlyAndIsIdempotent(t *testing.T) {
	root, unitDir := bindTree(t)
	if _, changed, err := WriteDeployBuildUser(root, unitDir, "acme-web"); err != nil || !changed {
		t.Fatalf("first write: changed %v, err %v", changed, err)
	}
	for _, runtime := range []string{"build", "clean"} {
		data, err := os.ReadFile(DeployBindDropInPath(unitDir, runtime, "acme-web"))
		if err != nil || string(data) != DeployBuildDropIn("acme-web") {
			t.Fatalf("%s drop-in %q, %v", runtime, data, err)
		}
		if strings.Contains(string(data), "SocketBind") || !strings.Contains(string(data), "User="+DeployBuildUserName("acme-web")) {
			t.Errorf("%s drop-in %q, want the user and no bind rule", runtime, data)
		}
	}
	if _, changed, err := WriteDeployBuildUser(root, unitDir, "acme-web"); err != nil || changed {
		t.Errorf("second write: changed %v, err %v", changed, err)
	}
	if _, _, err := WriteDeployBuildUser(root, unitDir, "../etc"); err == nil {
		t.Error("an invalid instance was written")
	}
	if err := ClearDeployBind(root, unitDir, "acme-web"); err != nil {
		t.Fatal(err)
	}
	for _, runtime := range []string{"build", "clean"} {
		if _, err := os.Stat(DeployBindDropInPath(unitDir, runtime, "acme-web")); !os.IsNotExist(err) {
			t.Errorf("the %s drop-in survived a clear: %v", runtime, err)
		}
	}
}

func TestValidate_buildUser(t *testing.T) {
	if _, err := Validate([]string{"deploy", "build-user", "acme-web"}); err != nil {
		t.Errorf("build-user refused: %v", err)
	}
	for _, argv := range [][]string{
		{"deploy", "build-user"},
		{"deploy", "build-user", "../etc"},
		{"deploy", "build-user", "acme-web", "extra"},
	} {
		if _, err := Validate(argv); err == nil {
			t.Errorf("%q allowed", argv)
		}
	}
}
