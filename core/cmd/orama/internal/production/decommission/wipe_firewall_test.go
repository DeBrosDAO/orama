package decommission

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The status of a node running tailscale, after an Orama install.
const numberedStatus = `Status: active

     To                         Action      From
     --                         ------      ----
[ 1] 22/tcp                     ALLOW IN    Anywhere                   # orama
[ 2] Anywhere on tailscale0     ALLOW IN    Anywhere
[ 3] 51820/udp                  ALLOW IN    Anywhere                   # orama
[ 4] 443/tcp                    ALLOW IN    Anywhere                   # orama
[ 5] 9100/tcp                   ALLOW IN    Anywhere
[ 6] Anywhere                   ALLOW IN    10.0.0.0/24                # orama
[ 7] 22/tcp (v6)                ALLOW IN    Anywhere (v6)              # orama
[ 8] 443/tcp (v6)               ALLOW IN    Anywhere (v6)              # orama
[ 9] Anywhere (v6) on tailscale0 ALLOW IN    Anywhere (v6)
`

// firewallBlock is the firewall section of the real wipe script.
func firewallBlock(t *testing.T) string {
	t.Helper()
	script := wipeScript(false)
	start := strings.Index(script, "# Remove the firewall rules Orama added")
	if start < 0 {
		t.Fatal("the wipe script has no firewall section")
	}
	end := strings.Index(script[start:], "\nfi\n")
	if end < 0 {
		t.Fatal("the firewall section does not end")
	}
	return script[start : start+end+len("\nfi\n")]
}

// runFirewallBlock runs the block with fake ufw and sshd and returns the rule
// numbers it deleted, in order. sshdPorts nil means sshd cannot be asked.
func runFirewallBlock(t *testing.T, sshdPorts []string) []string {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	dir := t.TempDir()
	deleted := filepath.Join(dir, "deleted")
	status := filepath.Join(dir, "status")
	if err := os.WriteFile(status, []byte(numberedStatus), 0o600); err != nil {
		t.Fatal(err)
	}
	fake := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	fake("ufw", `case "$1 $2" in
"status numbered") cat "`+status+`" ;;
"--force delete") echo "$3" >> "`+deleted+`" ;;
*) exit 1 ;;
esac
`)
	if sshdPorts != nil {
		var lines string
		for _, p := range sshdPorts {
			lines += "echo port " + p + "\n"
		}
		fake("sshd", "echo permitrootlogin no\n"+lines+"echo x11forwarding no\n")
	}
	cmd := exec.Command(bash, "-c", firewallBlock(t))
	cmd.Env = []string{"PATH=" + dir + ":/usr/bin:/bin"}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("firewall block failed: %v\n%s", err, out)
	}
	data, err := os.ReadFile(deleted)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(string(data))
}

// A reset deleted the operator's tailscale rule; only Orama's tagged rules go,
// newest first, and never the rule for the port this SSH session uses.
func TestWipeFirewall_removesOnlyOramasRulesAndKeepsSSH(t *testing.T) {
	got := runFirewallBlock(t, []string{"22"})
	if want := "8 6 4 3"; strings.Join(got, " ") != want {
		t.Fatalf("deleted rules %v, want %s (tagged, not ssh, highest number first)", got, want)
	}
}

func TestWipeFirewall_keepsEverySSHDPort(t *testing.T) {
	got := runFirewallBlock(t, []string{"22", "443"})
	if want := "6 3"; strings.Join(got, " ") != want {
		t.Fatalf("deleted rules %v, want %s: 443 is an sshd port here", got, want)
	}
}

// Not knowing which port SSH is on, deleting rules could lock the operator out.
func TestWipeFirewall_withoutSSHDTouchesNothing(t *testing.T) {
	if got := runFirewallBlock(t, nil); len(got) != 0 {
		t.Fatalf("deleted %v without knowing the SSH ports", got)
	}
}
