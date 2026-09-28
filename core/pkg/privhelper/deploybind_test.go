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
	for _, line := range strings.Split(DeployBindDropIn(10234), "\n") {
		if v, ok := strings.CutPrefix(line, "SocketBindAllow="); ok {
			allows = append(allows, v)
		}
	}
	if len(allows) != 2 || allows[0] != "" || allows[1] != "tcp:10234" {
		t.Fatalf("allows %q, want a reset then tcp:10234", allows)
	}
	if !strings.Contains(DeployBindDropIn(10234), "[Service]\n") {
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
	if err != nil || string(data) != DeployBindDropIn(10200) {
		t.Fatalf("drop-in %q, %v", data, err)
	}

	// The same port again: nothing to reload.
	if _, changed, err := WriteDeployBind(root, unitDir, "node", "acme-web", 10200); err != nil || changed {
		t.Fatalf("rewrite of the same port: changed %v, err %v", changed, err)
	}

	// A new port replaces the old one; the old one is not kept.
	prev, changed, err = WriteDeployBind(root, unitDir, "node", "acme-web", 10201)
	if err != nil || !changed || string(prev) != DeployBindDropIn(10200) {
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
	if data, _ := os.ReadFile(path); string(data) != DeployBindDropIn(10200) {
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
