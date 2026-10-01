package install

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/constants"
)

const productionOramaDir = "/opt/orama/.orama"

func gatewayTemplate(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	data, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "systemd", "orama-namespace-gateway@.service"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// directiveValues are the space-separated values of every key= line in unit,
// in order.
func directiveValues(unit, key string) []string {
	var out []string
	for _, line := range strings.Split(unit, "\n") {
		if rest, ok := strings.CutPrefix(line, key+"="); ok {
			out = append(out, strings.Fields(rest)...)
		}
	}
	return out
}

func inside(path string, dirs []string) bool {
	for _, d := range dirs {
		d = strings.TrimPrefix(d, "-")
		if path == d || strings.HasPrefix(path, d+"/") {
			return true
		}
	}
	return false
}

// The finding: every gateway could write all of data/ — the cluster gateway's
// signing key and every other namespace's state included — and read all of
// secrets/.
func TestGatewayTemplate_aTenantGatewayWritesItsOwnTreeOnly(t *testing.T) {
	unit := gatewayTemplate(t)
	writable := directiveValues(unit, "ReadWritePaths")
	for _, never := range []string{
		constants.DataDir(productionOramaDir),
		constants.NamespacesDir(productionOramaDir),
		constants.GatewayStateDir(constants.NamespacesDir(productionOramaDir), "index"),
		productionOramaDir + "/logs",
		productionOramaDir + "/tls-cache",
	} {
		if inside(never, writable) {
			t.Errorf("a tenant's gateway may write %s (ReadWritePaths %v)", never, writable)
		}
	}
	if !inside(constants.GatewayStateDir(constants.NamespacesDir(productionOramaDir), "%i"), writable) {
		t.Error("a gateway may not write its own state directory")
	}
	if got := directiveValues(unit, "TemporaryFileSystem"); strings.Join(got, " ") != productionOramaDir+"/secrets:ro" {
		t.Errorf("secrets/ is not hidden behind an empty tmpfs: %v", got)
	}
	binds := directiveValues(unit, "BindReadOnlyPaths")
	want := []string{
		productionOramaDir + "/secrets/cluster-secret",
		"-" + productionOramaDir + "/secrets/encryption-root",
		"-" + productionOramaDir + "/secrets/encryption-root.id",
		"-" + productionOramaDir + "/secrets/encryption-root.prev",
		"-" + productionOramaDir + "/secrets/encryption-root.prev.id",
	}
	if strings.Join(binds, " ") != strings.Join(want, " ") {
		t.Errorf("a gateway sees %v of secrets/, want exactly %v", binds, want)
	}
	if len(directiveValues(unit, "ReadOnlyPaths")) != 0 {
		t.Error("the template still exposes a directory read-only")
	}
}

// What only the cluster gateway writes is in its instance's drop-in, which
// also gives it back the whole secrets directory its join handler serves from.
func TestIndexGatewayDropIn(t *testing.T) {
	writable := directiveValues(IndexGatewayDropIn, "ReadWritePaths")
	for _, path := range []string{
		constants.GatewayStateDir(constants.NamespacesDir(productionOramaDir), "alice"),
		constants.HostTURNConfigPath(productionOramaDir),
	} {
		if !inside(path, writable) {
			t.Errorf("the cluster gateway may not write %s", path)
		}
	}
	for _, reset := range []string{"TemporaryFileSystem=\n", "BindReadOnlyPaths=\n"} {
		if !strings.Contains(IndexGatewayDropIn, reset) {
			t.Errorf("the drop-in does not reset %s", strings.TrimSpace(reset))
		}
	}
	if got := directiveValues(IndexGatewayDropIn, "ReadOnlyPaths"); strings.Join(got, " ") != productionOramaDir+"/secrets" {
		t.Errorf("ReadOnlyPaths = %v", got)
	}
	for _, cred := range []string{
		"jwt-signing-key.pem:/var/lib/orama-gateway-keys/index/jwt-signing-key.pem",
		"jwt-eddsa-key.pem:/var/lib/orama-gateway-keys/index/jwt-eddsa-key.pem",
	} {
		if !strings.Contains(IndexGatewayDropIn, "LoadCredential="+cred) {
			t.Errorf("the index gateway does not receive %s", cred)
		}
	}
	if strings.Contains(gatewayTemplate(t), "LoadCredential=jwt-signing-key") {
		t.Error("a tenant gateway receives the index signing key")
	}
	if !strings.HasSuffix(indexGatewayDropInDir, "/orama-namespace-gateway@index.service.d") {
		t.Errorf("the drop-in is not the index instance's: %s", indexGatewayDropInDir)
	}
}

// systemd rejects a "-" before the source of a LoadCredential= (it logs
// "Credential source ... not valid, ignoring" and the unit gets no credentials
// directory), so no unit this repo writes may carry one.
func TestLoadCredentialLines_haveNoOptionalPrefix(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	units, err := filepath.Glob(filepath.Join(filepath.Dir(file), "..", "..", "systemd", "*.service"))
	if err != nil {
		t.Fatal(err)
	}
	texts := map[string]string{"IndexGatewayDropIn": IndexGatewayDropIn}
	for _, u := range units {
		data, err := os.ReadFile(u)
		if err != nil {
			t.Fatal(err)
		}
		texts[filepath.Base(u)] = string(data)
	}
	credentials := 0
	for name, text := range texts {
		for _, line := range strings.Split(text, "\n") {
			rest, ok := strings.CutPrefix(line, "LoadCredential=")
			if !ok {
				continue
			}
			credentials++
			id, source, ok := strings.Cut(rest, ":")
			if !ok || id == "" || !strings.HasPrefix(source, "/") {
				t.Errorf("%s: %q is not LoadCredential=ID:/absolute/path", name, line)
			}
		}
	}
	if credentials == 0 {
		t.Fatal("found no LoadCredential= line to check")
	}
}

// TestIndexGatewayDropIn_credentialIDsAreTheFilesTheGatewayOpens: systemd
// names each credential file after its ID, and the gateway opens
// $CREDENTIALS_DIRECTORY/<file name>; the drop-in said jwt-signing-key, the
// gateway opened jwt-signing-key.pem, and the upgraded index gateway would not
// start.
func TestIndexGatewayDropIn_credentialIDsAreTheFilesTheGatewayOpens(t *testing.T) {
	ids := map[string]bool{}
	for _, line := range strings.Split(IndexGatewayDropIn, "\n") {
		rest, ok := strings.CutPrefix(line, "LoadCredential=")
		if !ok {
			continue
		}
		id, _, found := strings.Cut(rest, ":")
		if !found {
			t.Fatalf("LoadCredential line %q names no source", line)
		}
		ids[id] = true
	}
	for _, name := range []string{constants.GatewayRSAKeyFileName, constants.GatewayEdDSAKeyFileName} {
		if !ids[name] {
			t.Errorf("no credential named %s, the file the gateway opens; the drop-in loads %v", name, ids)
		}
	}
}
