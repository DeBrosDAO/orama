package install

import (
	"bytes"
	"strings"
	"testing"
)

// The banner told operators to run systemctl and journalctl, which bypass the
// dependency ordering and quorum checks the orama CLI exists for.
func TestLogSetupComplete_namesOnlyTheCLI(t *testing.T) {
	var out bytes.Buffer
	(&ProductionSetup{logWriter: &out}).LogSetupComplete("12D3KooWExample")

	got := out.String()
	for _, raw := range []string{"systemctl", "journalctl", "tail -f"} {
		if strings.Contains(got, raw) {
			t.Errorf("the banner still tells the operator to run %q:\\n%s", raw, got)
		}
	}
	for _, want := range []string{"12D3KooWExample", "orama node status", "orama node logs", "orama node report"} {
		if !strings.Contains(got, want) {
			t.Errorf("the banner does not mention %q:\\n%s", want, got)
		}
	}
}
