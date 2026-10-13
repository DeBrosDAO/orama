package upgrade

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/constants"
)

func TestFlagsResolve_aliasesAndURLs(t *testing.T) {
	cases := map[string]string{
		"letsencrypt":                       constants.LetsEncryptProductionACME,
		"letsencrypt-staging":               constants.LetsEncryptStagingACME,
		"https://ca.example/acme/directory": "https://ca.example/acme/directory",
		"":                                  "",
	}
	for in, want := range cases {
		f := &Flags{ACMECA: in}
		if err := f.Resolve(); err != nil || f.ACMECA != want {
			t.Errorf("Resolve(%q) = %q, %v; want %q", in, f.ACMECA, err, want)
		}
	}
}

// The value ends up in the Caddyfile root writes and in the command line a
// rolling upgrade runs on every node.
func TestFlagsResolve_refusesAnythingButAnHTTPSURL(t *testing.T) {
	for _, bad := range []string{"production", "http://ca.example/dir", "https://", "https://ca.example/dir }", "https://ca.example/dir x"} {
		f := &Flags{ACMECA: bad}
		if err := f.Resolve(); err == nil {
			t.Errorf("Resolve(%q) accepted it", bad)
		}
	}
}

// A rolling upgrade with --acme-ca passes it to every node's upgrade.
func TestUpgradeArgs_forwardTheACMECA(t *testing.T) {
	if got := upgradeArgs(&Flags{}); strings.Contains(got, "--acme-ca") {
		t.Errorf("--acme-ca forwarded when not given: %q", got)
	}
	got := upgradeArgs(&Flags{ACMECA: constants.LetsEncryptProductionACME})
	want := "--acme-ca '" + constants.LetsEncryptProductionACME + "'"
	if !strings.Contains(got, want) {
		t.Fatalf("upgradeArgs = %q, want it to contain %q", got, want)
	}
	// The shell the node runs sees one argument.
	out, err := exec.Command("bash", "-c", "printf '%s\\n' "+strings.SplitN(got, "--acme-ca ", 2)[1]).Output()
	if err != nil || strings.TrimSpace(string(out)) != constants.LetsEncryptProductionACME {
		t.Fatalf("the shell read %q, %v", out, err)
	}
}

func TestNewOrchestrator_recordsTheACMECA(t *testing.T) {
	o := NewOrchestrator(&Flags{ACMECA: constants.LetsEncryptProductionACME})
	got, err := o.setup.ACMECA()
	if err != nil || got != constants.LetsEncryptProductionACME {
		t.Fatalf("the upgrade's CA = %q, %v", got, err)
	}
}
