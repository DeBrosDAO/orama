package production

import (
	"os"
	"testing"
)

// ufwCommand must run ufw directly as root but via `sudo ufw` otherwise. At
// runtime the gateway runs as the unprivileged orama user, so without the sudo
// prefix AddWebRTCRules silently failed and TURN relay ports stayed firewalled
// after `webrtc enable`.
func TestUfwCommand_sudoPrefixDependsOnUID(t *testing.T) {
	cmd := ufwCommand("allow", "3478/udp")

	var want []string
	if os.Getuid() == 0 {
		want = []string{"ufw", "allow", "3478/udp"}
	} else {
		want = []string{"sudo", "ufw", "allow", "3478/udp"}
	}

	if len(cmd.Args) != len(want) {
		t.Fatalf("ufwCommand args = %v, want %v", cmd.Args, want)
	}
	for i := range want {
		if cmd.Args[i] != want[i] {
			t.Errorf("arg[%d] = %q, want %q (full: %v)", i, cmd.Args[i], want[i], cmd.Args)
		}
	}
}

// GenerateRules must open the TURN relay ports when TURN is enabled, so a node
// provisioned as a TURN host at install time is reachable.
func TestGenerateRules_turnPortsWhenEnabled(t *testing.T) {
	fp := NewFirewallProvisioner(FirewallConfig{
		SSHPort:        22,
		WireGuardPort:  51820,
		TURNEnabled:    true,
		TURNRelayStart: 49152,
		TURNRelayEnd:   65535,
	})
	rules := fp.GenerateRules()

	joined := ""
	for _, r := range rules {
		joined += r + "\n"
	}
	for _, want := range []string{"ufw allow 3478/udp", "ufw allow 3478/tcp", "ufw allow 5349/tcp", "ufw allow 49152:65535/udp"} {
		if !contains(rules, want) {
			t.Errorf("GenerateRules missing %q\nrules:\n%s", want, joined)
		}
	}
}

func TestGenerateRules_noTurnPortsWhenDisabled(t *testing.T) {
	fp := NewFirewallProvisioner(FirewallConfig{SSHPort: 22, WireGuardPort: 51820, TURNEnabled: false})
	for _, r := range fp.GenerateRules() {
		if r == "ufw allow 3478/udp" {
			t.Error("TURN ports must not be opened when TURNEnabled is false")
		}
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
