package inspector

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// rqlite.env is writable by the orama user and the namespace probe runs as
// root. A value that reached shell arithmetic unvalidated executed commands:
// bash evaluates array subscripts in $((...)), command substitutions included.
func TestNamespaceProbeScript_hostileEnvDoesNotExecute(t *testing.T) {
	for _, shell := range []string{"bash", "sh"} {
		if _, err := exec.LookPath(shell); err != nil {
			continue
		}
		t.Run(shell, func(t *testing.T) {
			dir := t.TempDir()
			marker := filepath.Join(dir, "PWNED")
			nsDir := filepath.Join(dir, "anchat")
			if err := os.MkdirAll(nsDir, 0755); err != nil {
				t.Fatal(err)
			}
			env := "HTTP_ADDR=10.0.0.1:x[$(touch " + marker + ")]\n" +
				"HTTP_ADV_ADDR=10.0.0.1:y[$(touch " + marker + ")]\n"
			if err := os.WriteFile(filepath.Join(nsDir, "rqlite.env"), []byte(env), 0600); err != nil {
				t.Fatal(err)
			}

			script, err := namespaceProbeScript("", dir, "anchat")
			if err != nil {
				t.Fatal(err)
			}
			out, _ := exec.Command(shell, "-c", script).Output()

			if _, err := os.Stat(marker); err == nil {
				t.Fatal("a command from rqlite.env was executed")
			}
			got := string(out)
			for _, want := range []string{"NS_START:anchat", "OLRIC:down", "GATEWAY:0", "NS_END"} {
				if !strings.Contains(got, want) {
					t.Errorf("output missing %q:\n%s", want, got)
				}
			}
			if strings.Contains(got, "RQLITE:") {
				t.Errorf("a hostile address was probed:\n%s", got)
			}
		})
	}
}

// Namespace names come from systemd unit names and are embedded in the root
// shell; anything that is not a namespace name is refused.
func TestNamespaceProbeScript_rejectsInvalidNames(t *testing.T) {
	for _, name := range []string{"", "a'b", "a;rm -rf /", "$(id)", " anchat", "../etc", strings.Repeat("a", 65)} {
		if _, err := namespaceProbeScript("", t.TempDir(), name); err == nil {
			t.Errorf("name %q accepted", name)
		}
	}
	if _, err := namespaceProbeScript("", t.TempDir(), "anchat-v2_test"); err != nil {
		t.Errorf("valid name refused: %v", err)
	}
}
