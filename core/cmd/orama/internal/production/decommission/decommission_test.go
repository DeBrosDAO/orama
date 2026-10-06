package decommission

import (
	"github.com/DeBrosOfficial/network/pkg/install"
	"os/exec"
	"strings"
	"testing"
)

// The wipe script's two fixes over the one it replaces.
func TestWipeScript(t *testing.T) {
	script := wipeScript(false)

	if !strings.Contains(script, `list-units --all --plain --no-legend "orama-namespace-*"`) {
		t.Error("the script must stop every namespace unit; those are template instances that match no legacy host unit name")
	}
	nsAt := strings.Index(script, "orama-namespace-*")
	rmAt := strings.Index(script, "rm -rf /opt/orama")
	if nsAt < 0 || rmAt < 0 || nsAt > rmAt {
		t.Error("namespace units must be stopped BEFORE the data directory is removed")
	}

	if strings.Contains(script, `pkill -9 -f "ipfs"`) {
		t.Error("an unanchored pkill pattern matches any command line mentioning ipfs")
	}
	if !strings.Contains(script, "/usr/local/bin/ipfs") {
		t.Error("the ipfs pkill pattern must be anchored to a full path")
	}

	if strings.Contains(script, "NUCLEAR=1") {
		t.Error("nuclear must be off unless asked for")
	}
	if !strings.Contains(wipeScript(true), "NUCLEAR=1") {
		t.Error("nuclear must be on when asked for")
	}
}

// The Anyone network was removed outright, relay keys included; a wipe of a
// node that never upgraded past it must not leave any of it behind.
func TestWipeScript_removesTheAnyoneNetwork(t *testing.T) {
	script := wipeScript(false)
	for _, want := range []string{
		"orama-namespace-anyone-client@index.service", "orama-anyone-relay.service",
		"for pkg in anon nyx; do",
		"/etc/anon", "/var/lib/anon", "/var/log/anon", "/etc/apt/sources.list.d/anon.list",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("wipe script missing %q", want)
		}
	}
	if strings.Contains(script, "DESTROY_ANON") {
		t.Error("Anyone relay keys are no longer preserved; the DESTROY_ANON switch must be gone")
	}
}

func TestWipeScript_removesTorConfigAndState(t *testing.T) {
	script := wipeScript(false)
	for _, want := range []string{"/etc/orama/tor", "/var/lib/orama-tor"} {
		if !strings.Contains(script, want) {
			t.Errorf("wipe script missing %q", want)
		}
	}
	if strings.Contains(script, "%!") {
		t.Errorf("wipe script has a formatting error:\n%s", script)
	}
}

func TestWipeScript_isValidBash(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	for _, nuclear := range []bool{false, true} {
		script := wipeScript(nuclear)
		inner, ok := strings.CutPrefix(script, "bash -c '")
		if !ok || !strings.HasSuffix(inner, "'") {
			t.Fatalf("wipe script is not a single bash -c '...' command")
		}
		inner = strings.TrimSuffix(inner, "'")
		if strings.Contains(inner, "'") {
			t.Fatal("a single quote inside the script would end the bash -c argument")
		}
		if out, err := exec.Command(bash, "-n", "-c", inner).CombinedOutput(); err != nil {
			t.Errorf("wipe script (nuclear=%v) is not valid bash: %v\n%s", nuclear, err, out)
		}
	}
}

// Only a nuclear wipe removes the Tor package and unmasks the distro units.
func TestWipeScript_nuclearPurgesTor(t *testing.T) {
	script := wipeScript(true)
	nuclearAt := strings.Index(script, `if [ -n "$NUCLEAR" ]`)
	purgeAt := strings.Index(script, "apt-get purge -y tor deb.torproject.org-keyring")
	unmaskAt := strings.Index(script, "systemctl unmask tor.service tor@default.service")
	if nuclearAt < 0 || purgeAt < nuclearAt || unmaskAt < nuclearAt {
		t.Errorf("tor purge (%d) and unmask (%d) must sit inside the nuclear block (%d)", purgeAt, unmaskAt, nuclearAt)
	}
	if !strings.Contains(script, "/etc/apt/sources.list.d/tor.sources") {
		t.Error("a nuclear wipe must remove the Tor apt source")
	}
}

// The privileged helper is how the orama group reaches root. A wipe must take
// it down — socket stopped before anything else, its unit files and binary
// removed — not leave a root entry point behind.
func TestWipeScript_removesThePrivilegedHelper(t *testing.T) {
	script := wipeScript(false)
	for _, want := range []string{
		"systemctl stop orama-privhelper.socket",
		"systemctl disable orama-privhelper.socket",
		"/etc/systemd/system/orama-privhelper.socket",
		"/etc/systemd/system/orama-privhelper@.service",
		"rm -f /usr/local/bin/orama-privhelper",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("wipe script missing %q", want)
		}
	}
	stopAt := strings.Index(script, "systemctl stop orama-privhelper.socket")
	nodeAt := strings.Index(script, "for svc in orama-node")
	if stopAt < 0 || nodeAt < 0 || stopAt > nodeAt {
		t.Error("the helper socket must be stopped before the rest of the node is torn down")
	}
}

// The namespace units' and deployments' env files live in root-owned trees
// outside /opt/orama; a wipe that left them would leave tenant secrets behind.
func TestWipeScript_stopsDeploymentUnits(t *testing.T) {
	script := wipeScript(false)
	if !strings.Contains(script, `"orama-deploy-*"`) {
		t.Fatal("the wipe does not stop orama-deploy units")
	}
	for _, dir := range []string{
		"/var/lib/private/orama-deploy-*",
		"/var/cache/private/orama-deploy-*",
		"/var/cache/private/orama-build",
	} {
		if !strings.Contains(script, dir) {
			t.Errorf("the wipe leaves %s", dir)
		}
	}
}

func TestWipeScript_removesTheRootOwnedEnvTrees(t *testing.T) {
	script := wipeScript(false)
	for _, dir := range []string{"/var/lib/orama-unit-env", "/var/lib/orama-deploy"} {
		if !strings.Contains(script, dir) {
			t.Errorf("the wipe script does not remove %s", dir)
		}
	}
}

func TestWipeScript_removesTheArchiveTrustAnchor(t *testing.T) {
	script := wipeScript(false)
	if !strings.Contains(script, "rm -f /etc/orama/archive-signers /etc/orama/archive-signers.rotated") {
		t.Fatalf("a wiped node keeps trusting its old cluster's signers:\n%s", script)
	}
}

// A wiped node kept /var/lib/caddy: the old certificate was served on the next
// install, and the TLS and ACME account private keys stayed on the machine.
func TestWipeScript_removesCaddyStorage(t *testing.T) {
	if !strings.Contains(wipeScript(false), "rm -rf /var/lib/caddy\n") {
		t.Error("the wipe script leaves Caddy's certificates and private keys behind")
	}
}

// Install writes drop-ins for the cluster gateway's instance and the build
// template, and the build's resolver file; a wiped node keeps none of them.
func TestWipeScript_removesInstallsDropIns(t *testing.T) {
	script := wipeScript(false)
	for _, want := range []string{
		"rm -rf /etc/systemd/system/orama-namespace-gateway@index.service.d /etc/systemd/system/orama-deploy-build@.service.d\n",
		"rm -f " + install.BuildResolvConfPath + "\n",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("the wipe script lacks %q", strings.TrimSpace(want))
		}
	}
}
