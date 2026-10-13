package legacylayout

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeStager records what would be handed to orama-privhelper.
type fakeStager struct {
	unitEnvs map[string]string // "<ns>/<svc>" → contents
	envs     map[string]string // instance → env
	tokens   map[string]string // instance → token
	err      error
}

func newFakeStager() *fakeStager {
	return &fakeStager{unitEnvs: map[string]string{}, envs: map[string]string{}, tokens: map[string]string{}}
}

func (f *fakeStager) SetUnitEnv(namespace, service, contents string) error {
	if f.err != nil {
		return f.err
	}
	f.unitEnvs[namespace+"/"+service] = contents
	return nil
}

func (f *fakeStager) SetDeploymentEnv(instance, contents string) error {
	if f.err != nil {
		return f.err
	}
	f.envs[instance] = contents
	return nil
}

func (f *fakeStager) SetDeploymentToken(instance, token string) error {
	if f.err != nil {
		return f.err
	}
	f.tokens[instance] = token
	return nil
}

func (f *fakeStager) calls() int { return len(f.unitEnvs) + len(f.envs) + len(f.tokens) }

var errHelperDown = errors.New("orama-privhelper.socket refused the connection")

// fixture is an orama directory and a unit env tree in a temp dir.
type fixture struct {
	oramaDir, unitEnvDir, systemdDir string
	stager                           *fakeStager
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	f := &fixture{oramaDir: filepath.Join(root, ".orama"), unitEnvDir: filepath.Join(root, "unit-env"),
		systemdDir: filepath.Join(root, "systemd"), stager: newFakeStager()}
	for _, d := range []string{"secrets", "configs", "data"} {
		if err := os.MkdirAll(filepath.Join(f.oramaDir, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f *fixture) path(rel string) string { return filepath.Join(f.oramaDir, rel) }

func (f *fixture) write(t *testing.T, rel, content string) {
	t.Helper()
	writeFile(t, f.path(rel), content)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) migrator() Migrator {
	return Migrator{OramaDir: f.oramaDir, UnitEnvDir: f.unitEnvDir, SystemdUnitDir: f.systemdDir, Stager: f.stager}
}

func (f *fixture) run(t *testing.T) error {
	t.Helper()
	return f.migrator().Run()
}

// seedOldLayout writes everything a 0.122.x node holds on the old layout.
func (f *fixture) seedOldLayout(t *testing.T) {
	t.Helper()
	f.write(t, "secrets/jwt-signing-key.pem", "rsa")
	f.write(t, "secrets/jwt-eddsa-key.pem", "ed")
	f.write(t, "sqlite/acme/app.db", "db")
	f.write(t, "deployments/acme/web/app", "bin")
	f.write(t, "configs/turn.yaml", "turn")
	f.write(t, "data/namespaces/acme/rqlite.env", "HTTP_ADDR=10.0.0.1:10200\n")
	f.write(t, "data/namespaces/acme/gateway.env", "GATEWAY_CONFIG=/x.yaml\n")
	f.write(t, "deployment-env/orama-deploy-acme-web.env", "PORT=10500\n")
	f.write(t, "deployment-env/orama-deploy-acme-web.token", "tok")
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func assertGone(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Errorf("%s still exists (%v)", path, err)
	}
}

func assertNamesBoth(t *testing.T, err error, a, b string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected a refusal naming %s and %s", a, b)
	}
	if !strings.Contains(err.Error(), a) || !strings.Contains(err.Error(), b) {
		t.Fatalf("error must name both %s and %s: %v", a, b, err)
	}
}
