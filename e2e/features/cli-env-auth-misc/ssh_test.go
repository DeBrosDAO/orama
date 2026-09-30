//go:build e2e_fleet

package clienvauthmisc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
)

// sshBudget bounds one `orama ssh` round trip (key from the agent, connect, run).
const sshBudget = 2 * time.Minute

// sshMarker is what the remote command prints, so a success is the command
// having run on the node and nothing else.
const sshMarker = "e2e-orama-ssh-ok"

// hostKeyRefusals are what OpenSSH (which orama ssh runs, its stderr passed
// through) says when it refuses a host key: a key that changed from the
// pinned one, or one it has no pin for under strict checking. A refusal for
// another reason (no key, no route) is not the host-key check under test.
var hostKeyRefusals = []string{"REMOTE HOST IDENTIFICATION HAS CHANGED", "Host key verification failed", "host key is known for"}

// expectHostKeyRefusal fails unless res was refused by host-key verification.
func expectHostKeyRefusal(t testing.TB, res oramacli.Result, n fleet.Node) {
	t.Helper()
	text := output(res)
	for _, want := range hostKeyRefusals {
		if strings.Contains(text, want) {
			return
		}
	}
	t.Errorf("orama ssh to %s was not refused for its host key (want one of %q):\n%s", n.PublicIP, hostKeyRefusals, text)
}

// knownHostsPaths are where the CLI and OpenSSH look for pinned host keys in
// a HOME (docs/DEVNET_INSTALL.md "~/.orama/known_hosts"; ssh's default).
var knownHostsPaths = []string{".orama/known_hosts", ".ssh/known_hosts"}

// sshHome is an isolated HOME whose known_hosts files hold lines.
func sshHome(t testing.TB, lines []string) *oramacli.Runner {
	t.Helper()
	cli := isolated(t)
	for _, rel := range knownHostsPaths {
		path := filepath.Join(cli.Home, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return cli
}

// pinnedLines returns the run's pinned known_hosts lines for n.
func pinnedLines(t testing.TB, st *fleet.State, n fleet.Node) []string {
	t.Helper()
	raw, err := os.ReadFile(st.KnownHostsFile)
	if err != nil {
		t.Fatalf("failed to read the run's pinned host keys: %v", err)
	}
	var out []string
	for _, l := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(l)
		if len(fields) >= 3 && hostsField(fields[0], n.PublicIP) {
			out = append(out, l)
		}
	}
	if len(out) == 0 {
		t.Fatalf("the run pinned no host key for %s", n.PublicIP)
	}
	return out
}

func hostsField(field, ip string) bool {
	for _, h := range strings.Split(field, ",") {
		if h == ip || h == "["+ip+"]:22" {
			return true
		}
	}
	return false
}

func sshRun(t testing.TB, cli *oramacli.Runner, env string, n fleet.Node) oramacli.Result {
	t.Helper()
	return infra.RunFor(t, cli, sshBudget, "ssh", "--env", env, n.PublicIP, "echo "+sshMarker)
}

// TestSSH_runsCommandOnPinnedNode: `orama ssh <ip> '<command>'` runs the
// command on that node non-interactively (docs/CLI_REFERENCE.md#orama-ssh),
// with the key from RootWallet and the node's pinned host key.
func TestSSH_runsCommandOnPinnedNode(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := f.State.Nodes[0]
	res := sshRun(t, sshHome(t, pinnedLines(t, f.State, n)), f.State.Env, n)
	infra.ExpectExit(t, res, exitOK, sshMarker)
}

// TestSSH_refusesUnpinnedHostKey: the CLI's own SSH must verify the node
// against pinned host keys and never trust on first use (e2e/README.md
// "oramacli": "must verify hosts against the run's pinned known_hosts, never
// trust on first use"; docs/DEVNET_INSTALL.md pins host keys at setup). A
// HOME that pinned nothing must not run the command.
func TestSSH_refusesUnpinnedHostKey(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := f.State.Nodes[0]
	cli := isolated(t)
	res := sshRun(t, cli, f.State.Env, n)
	if res.Exit == exitOK || strings.Contains(res.Stdout, sshMarker) {
		t.Errorf("orama ssh ran a command on %s without a pinned host key (trust on first use):\n%s", n.PublicIP, output(res))
	} else {
		expectHostKeyRefusal(t, res, n)
	}
	if raw, err := os.ReadFile(filepath.Join(cli.Home, ".ssh", "known_hosts")); err == nil && strings.Contains(string(raw), n.PublicIP) {
		t.Errorf("orama ssh pinned %s's host key on first contact:\n%s", n.PublicIP, raw)
	}
}

// TestSSH_refusesWrongHostKey: a node presenting a key other than the one
// pinned for its address is refused (a man in the middle, or a recycled IP).
func TestSSH_refusesWrongHostKey(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	if len(f.State.Nodes) < 2 {
		t.Fatalf("the fleet has %d nodes, this test needs two", len(f.State.Nodes))
	}
	victim, other := f.State.Nodes[0], f.State.Nodes[1]
	var forged []string
	for _, l := range pinnedLines(t, f.State, other) {
		fields := strings.Fields(l)
		forged = append(forged, victim.PublicIP+" "+strings.Join(fields[1:], " "))
	}
	res := sshRun(t, sshHome(t, forged), f.State.Env, victim)
	if res.Exit == exitOK || strings.Contains(res.Stdout, sshMarker) {
		t.Errorf("orama ssh accepted %s under %s's pinned key:\n%s", victim.PublicIP, other.PublicIP, output(res))
	} else {
		expectHostKeyRefusal(t, res, victim)
	}
}

// TestSSH_noWalletHasNoKey: without the RootWallet agent there is no SSH key
// to use, so nothing runs.
func TestSSH_noWalletHasNoKey(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := f.State.Nodes[0]
	res := sshRun(t, harness.CLI(t).NoWallet(t), f.State.Env, n)
	if res.Exit == exitOK || strings.Contains(res.Stdout, sshMarker) {
		t.Errorf("orama ssh ran a command with no wallet on the machine:\n%s", output(res))
	}
}
