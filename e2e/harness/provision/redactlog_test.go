package provision

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/secrets"
)

// TestExecCommander_logAndTailRedacted: what a provisioning command prints
// reaches its log and the error that quotes it redacted: a registered
// value and a credential shape both.
func TestExecCommander_logAndTailRedacted(t *testing.T) {
	log := filepath.Join(t.TempDir(), "provision-01-x.log")
	red := secrets.NewRedactor("wallet-pw-12345678")
	script := `echo "pw wallet-pw-12345678"; echo '{"access_token":"at-12345678"}' >&2; exit 3`
	_, err := execCommander{}.Run(context.Background(), command{name: "sh", args: []string{"-c", script},
		env: []string{"PATH=" + os.Getenv("PATH")}, log: log, redact: red.Redact})
	if err == nil {
		t.Fatal("the failing command succeeded")
	}
	raw, rerr := os.ReadFile(log)
	if rerr != nil {
		t.Fatal(rerr)
	}
	for _, got := range []string{string(raw), err.Error()} {
		if strings.Contains(got, "wallet-pw-12345678") || strings.Contains(got, "at-12345678") || !strings.Contains(got, secrets.Mask) {
			t.Fatalf("not redacted: %q", got)
		}
	}
}

// TestExecCommander_defaultRedactsTheEnvironmentsSecrets: a command with
// no redactor of its own still masks the run's secret variables.
func TestExecCommander_defaultRedactsTheEnvironmentsSecrets(t *testing.T) {
	t.Setenv("HCLOUD_TOKEN", "hc-in-a-log-12345")
	_, err := execCommander{}.Run(context.Background(), command{name: "sh", args: []string{"-c", "echo hc-in-a-log-12345; exit 1"},
		env: []string{"PATH=" + os.Getenv("PATH")}})
	if err == nil || strings.Contains(err.Error(), "hc-in-a-log-12345") {
		t.Fatalf("err %v", err)
	}
}
