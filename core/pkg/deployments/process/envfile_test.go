package process

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/deployments"
	"go.uber.org/zap"
)

func testManager(t *testing.T) (*Manager, string) {
	t.Helper()
	envDir := filepath.Join(t.TempDir(), "deployment-env")
	return NewManager(zap.NewNop(), Config{EnvDir: envDir, BaseDomain: "dbrs.space"}), envDir
}

func testDeployment() *deployments.Deployment {
	return &deployments.Deployment{
		Namespace: "acme",
		Name:      "api",
		Port:      8080,
		Environment: map[string]string{
			"DATABASE_URL": "postgres://u:p@h/db",
			"NOTE":         "he said \"hi\"\nand left",
		},
	}
}

// The environment holds the tenant's secrets, and the deployment's own
// directory is world-readable so the unprivileged user it runs as can read the
// code. The two must not be the same place, and the file must not be readable
// by anyone but root.
func TestWriteEnvFile_isReadableOnlyByRoot(t *testing.T) {
	m, envDir := testManager(t)

	path, err := m.writeEnvFile(testDeployment(), "orama-deploy-acme-api")
	if err != nil {
		t.Fatalf("writeEnvFile: %v", err)
	}
	if filepath.Dir(path) != envDir {
		t.Fatalf("the environment file landed in %s, not in the environment directory", filepath.Dir(path))
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("the environment file is mode %04o, want 0600", perm)
	}

	dirInfo, err := os.Stat(envDir)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0700 {
		t.Errorf("the environment directory is mode %04o, want 0700", perm)
	}
}

// A directory left over from an earlier version, or created with a loose umask,
// is tightened rather than trusted.
func TestWriteEnvFile_tightensAnExistingLooseDirectory(t *testing.T) {
	m, envDir := testManager(t)
	if err := os.MkdirAll(envDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if _, err := m.writeEnvFile(testDeployment(), "orama-deploy-acme-api"); err != nil {
		t.Fatalf("writeEnvFile: %v", err)
	}
	info, err := os.Stat(envDir)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0700 {
		t.Errorf("an existing world-readable directory was left at %04o", perm)
	}
}

func TestWriteEnvFile_writesTheTenantsValuesAndThePlatforms(t *testing.T) {
	m, _ := testManager(t)

	path, err := m.writeEnvFile(testDeployment(), "orama-deploy-acme-api")
	if err != nil {
		t.Fatalf("writeEnvFile: %v", err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	got := systemdReadEnvFile(string(contents))

	for key, want := range map[string]string{
		"DATABASE_URL":      "postgres://u:p@h/db",
		"NOTE":              "he said \"hi\"\nand left",
		"PORT":              "8080",
		"ORAMA_NAMESPACE":   "acme",
		"ORAMA_GATEWAY_URL": "https://ns-acme.dbrs.space",
		"ORAMA_STATE_DIR":   "/var/lib/orama-deploy-acme-api",
		"ORAMA_CACHE_DIR":   "/var/cache/orama-deploy-acme-api",
	} {
		if got[key] != want {
			t.Errorf("%s = %q, want %q\nfile:\n%s", key, got[key], want, contents)
		}
	}
}

func TestWriteEnvFile_refusesWithNowhereSafeToPutIt(t *testing.T) {
	m := NewManager(zap.NewNop(), Config{BaseDomain: "dbrs.space"})
	_, err := m.writeEnvFile(testDeployment(), "orama-deploy-acme-api")
	if err == nil {
		t.Fatal("the environment was written with no directory configured")
	}
	// The refusal has to say what is wrong. Falling through to a bare mkdir
	// failure reports "mkdir : no such file or directory", which reads like a
	// bug in the deployment rather than a gateway that was never configured.
	if !strings.Contains(err.Error(), "no environment directory is configured") {
		t.Errorf("the refusal does not say what is missing: %v", err)
	}
}

func TestWriteEnvFile_refusesAValueItCouldNotDeliver(t *testing.T) {
	m, envDir := testManager(t)
	d := testDeployment()
	d.Environment["BROKEN"] = "\xff"

	if _, err := m.writeEnvFile(d, "orama-deploy-acme-api"); err == nil {
		t.Fatal("a value systemd would discard was written anyway")
	}
	if _, err := os.Stat(filepath.Join(envDir, "orama-deploy-acme-api.env")); !os.IsNotExist(err) {
		t.Error("a rejected environment still left a file behind")
	}
}

// The secrets must not outlive the deployment.
func TestRemoveEnvFile_takesTheSecretsOffTheNode(t *testing.T) {
	m, _ := testManager(t)
	path, err := m.writeEnvFile(testDeployment(), "orama-deploy-acme-api")
	if err != nil {
		t.Fatalf("writeEnvFile: %v", err)
	}

	if err := m.removeEnvFile("orama-deploy-acme-api"); err != nil {
		t.Fatalf("removeEnvFile: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("the environment file is still on disk after the deployment was stopped")
	}

	// Stopping a deployment twice, or one that never wrote a file, is not an
	// error to report.
	if err := m.removeEnvFile("orama-deploy-acme-api"); err != nil {
		t.Errorf("removing an absent environment file reported %v", err)
	}
}

func TestGatewayURL(t *testing.T) {
	m, _ := testManager(t)
	if got := m.gatewayURL("acme"); got != "https://ns-acme.dbrs.space" {
		t.Errorf("gatewayURL = %q", got)
	}
	if got := m.gatewayURL(""); got != "" {
		t.Errorf("gatewayURL with no namespace = %q, want empty", got)
	}

	noDomain := NewManager(zap.NewNop(), Config{EnvDir: t.TempDir()})
	if got := noDomain.gatewayURL("acme"); got != "" {
		t.Errorf("gatewayURL with no base domain = %q, want empty", got)
	}
}

// systemdReadEnvFile is the same transcription of systemd's environment-file
// parser the encoder is tested against, reduced to what these tests need: it
// reads back what systemd would read, not what the writer meant.
func systemdReadEnvFile(contents string) map[string]string {
	out := map[string]string{}
	for _, assignment := range splitAssignments(contents) {
		eq := strings.IndexByte(assignment, '=')
		if eq < 0 {
			continue
		}
		key, raw := assignment[:eq], assignment[eq+1:]
		out[key] = unquoteEnvValue(raw)
	}
	return out
}

// splitAssignments splits on the newlines that are not inside a quoted value.
func splitAssignments(contents string) []string {
	var out []string
	var current strings.Builder
	inQuotes, escaped := false, false
	for i := 0; i < len(contents); i++ {
		c := contents[i]
		switch {
		case escaped:
			escaped = false
			current.WriteByte(c)
		case c == '\\' && inQuotes:
			escaped = true
			current.WriteByte(c)
		case c == '"':
			inQuotes = !inQuotes
			current.WriteByte(c)
		case c == '\n' && !inQuotes:
			if current.Len() > 0 {
				out = append(out, current.String())
				current.Reset()
			}
		default:
			current.WriteByte(c)
		}
	}
	if current.Len() > 0 {
		out = append(out, current.String())
	}
	return out
}

func unquoteEnvValue(raw string) string {
	if len(raw) < 2 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		return raw
	}
	inner := raw[1 : len(raw)-1]
	var b strings.Builder
	for i := 0; i < len(inner); i++ {
		if inner[i] == '\\' && i+1 < len(inner) && strings.IndexByte("\"\\`$", inner[i+1]) >= 0 {
			i++
			b.WriteByte(inner[i])
			continue
		}
		b.WriteByte(inner[i])
	}
	return b.String()
}

// os.WriteFile only sets the mode when it creates the file, so a file left at a
// looser mode by an earlier version would keep it and go on holding the
// tenant's secrets where anyone on the node can read them.
func TestWriteEnvFile_tightensAnExistingLooseFile(t *testing.T) {
	m, envDir := testManager(t)
	if err := os.MkdirAll(envDir, 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	stale := filepath.Join(envDir, "orama-deploy-acme-api.env")
	if err := os.WriteFile(stale, []byte("OLD=\"1\"\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, err := m.writeEnvFile(testDeployment(), "orama-deploy-acme-api"); err != nil {
		t.Fatalf("writeEnvFile: %v", err)
	}
	info, err := os.Stat(stale)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("an existing world-readable environment file was left at %04o", perm)
	}
}

// The direct runner is what runs a deployment off systemd. It has to hand the
// app the same environment the unit would, or an app behaves differently
// depending on which runner started it.
func TestStartDirect_givesTheAppTheSameEnvironmentTheUnitWould(t *testing.T) {
	m, _ := testManager(t)
	workDir := t.TempDir()
	dumped := filepath.Join(workDir, "env.txt")

	script := "#!/bin/sh\nenv > " + dumped + "\n"
	if err := os.WriteFile(filepath.Join(workDir, "app"), []byte(script), 0755); err != nil {
		t.Fatalf("write app: %v", err)
	}

	d := testDeployment()
	d.Type = deployments.DeploymentTypeGoBackend
	if err := m.startDirect(context.Background(), d, workDir); err != nil {
		t.Fatalf("startDirect: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	var contents []byte
	for time.Now().Before(deadline) {
		var err error
		contents, err = os.ReadFile(dumped)
		if err == nil && len(contents) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(contents) == 0 {
		t.Fatal("the spawned process never wrote its environment")
	}

	env := string(contents)
	for _, want := range []string{
		"PORT=8080",
		"ORAMA_NAMESPACE=acme",
		"ORAMA_GATEWAY_URL=https://ns-acme.dbrs.space",
		"ORAMA_STATE_DIR=/var/lib/orama-deploy-acme-api",
		"ORAMA_CACHE_DIR=/var/cache/orama-deploy-acme-api",
		"DATABASE_URL=postgres://u:p@h/db",
	} {
		if !strings.Contains(env, want) {
			t.Errorf("the process did not get %s\nenvironment:\n%s", want, env)
		}
	}
}
