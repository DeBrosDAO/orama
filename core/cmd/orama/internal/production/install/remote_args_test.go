package install

import (
	"bytes"
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

// The list of flags to forward was written out by hand and had drifted:
// --ca-fingerprint, --environment, --ssh-user, --operator-wallet, --peers and
// the four --ipfs-* flags never reached the node. Dropping --ca-fingerprint is
// the one that matters — the joining node then has nothing to pin the
// cluster's certificate against and falls back to trust-on-first-use, so a
// laptop-driven join quietly did not get the verification that was asked for.

// A Flags with every field set to something recognisable.
func fullFlags() *Flags {
	return &Flags{
		VpsIP:                "1.2.3.4",
		Domain:               "node1.example.com",
		BaseDomain:           "example.com",
		JoinAddress:          "https://node0.example.com",
		Token:                "tok",
		CAFingerprint:        "fp",
		JoinSNI:              "stagenet.example",
		SSHUser:              "ubuntu",
		Environment:          "devnet",
		OperatorWallet:       "0xabc",
		ACMECA:               "https://acme-staging-v02.api.letsencrypt.org/directory",
		PeersStr:             "/ip4/10.0.0.1/tcp/4001/p2p/Qm",
		IPFSPeerID:           "QmPeer",
		IPFSAddrs:            "/ip4/10.0.0.1/tcp/4001",
		IPFSClusterPeerID:    "QmCluster",
		IPFSClusterAddrs:     "/ip4/10.0.0.1/tcp/9096",
		ClusterSecret:        "cs",
		SwarmKey:             "sk",
		Nameserver:           true,
		Force:                true,
		SkipChecks:           true,
		SkipFirewall:         true,
		DryRun:               true,
		Archive:              "/tmp/orama-9.9.9-linux-amd64.tar.gz",
		ExpectArchiveSigners: "0x2222222222222222222222222222222222222222",
	}
}

// localOnlyFields stay on the operator's machine by design.
var localOnlyFields = map[string]bool{
	// The node installs the archive that was uploaded; the path is local.
	"Archive": true,
}

func TestRemoteInstallArgs_forwardsEveryFlag(t *testing.T) {
	line := strings.Join(remoteInstallArgs(fullFlags()), " ")

	for _, want := range []string{
		"--vps-ip 1.2.3.4",
		"--domain node1.example.com",
		"--base-domain example.com",
		"--join https://node0.example.com",
		"--ca-fingerprint fp",
		"--ssh-user ubuntu",
		"--environment devnet",
		"--operator-wallet 0xabc",
		"--peers /ip4/10.0.0.1/tcp/4001/p2p/Qm",
		"--ipfs-peer QmPeer",
		"--ipfs-addrs /ip4/10.0.0.1/tcp/4001",
		"--ipfs-cluster-peer QmCluster",
		"--ipfs-cluster-addrs /ip4/10.0.0.1/tcp/9096",
		"--nameserver",
		"--force",
		"--skip-checks",
		"--skip-firewall",
		"--dry-run",
		"--secrets-stdin",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("%q is not forwarded:\n%s", want, line)
		}
	}
}

// secretFields are the Flags that reach the node on stdin, never its argv.
var secretFields = map[string]bool{"Token": true, "ClusterSecret": true, "SwarmKey": true}

// Every string field of Flags has to reach the node — on the command line, or
// for a secret on stdin — so adding a flag and forgetting to forward it is
// caught here rather than by a node that installs without it.
func TestRemoteInstallArgs_coversEveryStringField(t *testing.T) {
	line := strings.Join(remoteInstallArgs(fullFlags()), " ")
	payload, err := remoteSecrets(fullFlags())
	if err != nil {
		t.Fatal(err)
	}

	v := reflect.ValueOf(*fullFlags())
	typ := v.Type()
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if field.Type.Kind() != reflect.String {
			continue
		}
		value := v.Field(i).String()
		if value == "" {
			t.Fatalf("fullFlags leaves %s empty; the test cannot check it", field.Name)
		}
		if localOnlyFields[field.Name] {
			if strings.Contains(line, value) {
				t.Errorf("Flags.%s (%q) is forwarded to the node but is local only:\n%s", field.Name, value, line)
			}
			continue
		}
		if secretFields[field.Name] {
			if !strings.Contains(string(payload), `"`+value+`"`) {
				t.Errorf("Flags.%s (%q) is not in the stdin secrets:\n%s", field.Name, value, payload)
			}
			continue
		}
		if !strings.Contains(line, value) {
			t.Errorf("Flags.%s (%q) is never forwarded to the node:\n%s", field.Name, value, line)
		}
	}
}

// A secret on the node's command line is readable by every local user in ps
// for as long as the install runs.
func TestRemoteInstallArgs_neverPutsASecretInArgv(t *testing.T) {
	flags := fullFlags()
	flags.Token = "invite-token-9f2c"
	flags.ClusterSecret = "cluster-secret-77ab"
	flags.SwarmKey = "swarm-key-c0de"
	line := joinShellArgs(remoteInstallArgs(flags))
	for _, secret := range []string{flags.Token, flags.ClusterSecret, flags.SwarmKey, "--token", "--cluster-secret", "--swarm-key"} {
		if strings.Contains(line, secret) {
			t.Errorf("%q is on the node's command line:\n%s", secret, line)
		}
	}
}

// With no secret there is nothing to read, and the node must not wait on stdin.
func TestRemoteInstallArgs_noSecretsNoStdin(t *testing.T) {
	flags := &Flags{VpsIP: "1.2.3.4", JoinAddress: "https://node0.example.com"}
	if line := strings.Join(remoteInstallArgs(flags), " "); strings.Contains(line, "--secrets-stdin") {
		t.Errorf("--secrets-stdin without a secret: %s", line)
	}
	if payload, err := remoteSecrets(flags); err != nil || payload != nil {
		t.Errorf("remoteSecrets = %q, %v; want nothing", payload, err)
	}
}

// What the laptop writes is what the node reads back.
func TestStdinSecrets_roundTrip(t *testing.T) {
	sent := fullFlags()
	payload, err := remoteSecrets(sent)
	if err != nil {
		t.Fatal(err)
	}
	node := &Flags{SecretsFromStdin: true}
	if err := node.readStdinSecrets(bytes.NewReader(payload)); err != nil {
		t.Fatal(err)
	}
	if node.Token != sent.Token || node.ClusterSecret != sent.ClusterSecret || node.SwarmKey != sent.SwarmKey {
		t.Errorf("node read %+v, laptop sent token=%q cluster=%q swarm=%q", node, sent.Token, sent.ClusterSecret, sent.SwarmKey)
	}
}

func TestReadStdinSecrets_refusesBadInput(t *testing.T) {
	for name, c := range map[string]struct {
		flags Flags
		stdin string
	}{
		"empty stdin":         {Flags{SecretsFromStdin: true}, ""},
		"not json":            {Flags{SecretsFromStdin: true}, "tok"},
		"empty object":        {Flags{SecretsFromStdin: true}, "{}"},
		"unknown field":       {Flags{SecretsFromStdin: true}, `{"token":"t","password":"p"}`},
		"two objects":         {Flags{SecretsFromStdin: true}, `{"token":"t"}{"token":"u"}`},
		"also on argv":        {Flags{SecretsFromStdin: true, Token: "argv"}, `{"token":"t"}`},
		"with remote":         {Flags{SecretsFromStdin: true, Remote: true}, `{"token":"t"}`},
		"larger than the cap": {Flags{SecretsFromStdin: true}, `{"token":"` + strings.Repeat("a", maxStdinSecretsBytes) + `"}`},
	} {
		t.Run(name, func(t *testing.T) {
			f := c.flags
			if err := f.readStdinSecrets(strings.NewReader(c.stdin)); err == nil {
				t.Errorf("accepted %q", c.stdin)
			}
		})
	}
}

// Without the flag stdin is not touched: a local install may be interactive.
func TestReadStdinSecrets_offLeavesStdinAlone(t *testing.T) {
	f := &Flags{Token: "argv"}
	r := strings.NewReader(`{"token":"t"}`)
	if err := f.readStdinSecrets(r); err != nil {
		t.Fatal(err)
	}
	if f.Token != "argv" || r.Len() == 0 {
		t.Errorf("stdin was read without --secrets-stdin (token=%q, unread=%d)", f.Token, r.Len())
	}
}

// Remote is about which machine runs the install, not about how it runs, so it
// must not be forwarded — the node would then try to SSH somewhere itself.
func TestRemoteInstallArgs_doesNotForwardRemote(t *testing.T) {
	flags := fullFlags()
	flags.Remote = true

	if line := strings.Join(remoteInstallArgs(flags), " "); strings.Contains(line, "--remote") {
		t.Errorf("--remote must not be forwarded:\n%s", line)
	}
}

func TestRemoteInstallArgs_omitsEmptyFlags(t *testing.T) {
	line := strings.Join(remoteInstallArgs(&Flags{VpsIP: "1.2.3.4"}), " ")

	if line != "--vps-ip 1.2.3.4" {
		t.Errorf("got %q, want only the flag that was set", line)
	}
}

// A value with a space or a quote has to survive being pasted into an SSH
// command line.
func TestJoinShellArgs_quotesWhatNeedsIt(t *testing.T) {
	got := joinShellArgs([]string{"orama", "node", "install", "--domain", "a b"})
	if !strings.Contains(got, "'a b'") {
		t.Errorf("a value with a space must be quoted: %s", got)
	}
}

// Invite fields reach `sudo orama node install ...` on the joining machine.
// Every value must arrive as exactly the one argument it was, whatever it holds.
func TestJoinShellArgs_survivesAHostileValue(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not available")
	}
	args := []string{
		"--join-sni", "x';touch /tmp/pwned;'",
		"--token", "abc\ntouch /tmp/pwned",
		"--join", "https://1.2.3.4",
		"--domain", "$(id)",
		"--empty", "",
		"--quote", `a"b'c`,
	}
	out, err := exec.Command(sh, "-c", `printf '%s\0' `+joinShellArgs(args)).Output()
	if err != nil {
		t.Fatalf("the joined line is not valid shell: %v", err)
	}
	got := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
	if len(got) != len(args) {
		t.Fatalf("shell saw %d arguments, want %d: %q", len(got), len(args), got)
	}
	for i := range args {
		if got[i] != args[i] {
			t.Errorf("argument %d arrived as %q, want %q", i, got[i], args[i])
		}
	}
}

func TestJoinShellArgs_leavesPlainWordsBare(t *testing.T) {
	got := joinShellArgs([]string{"--vps-ip", "1.2.3.4", "--join", "https://1.2.3.4:443"})
	if got != "--vps-ip 1.2.3.4 --join https://1.2.3.4:443" {
		t.Errorf("plain words were quoted: %s", got)
	}
}

func TestRemoteArchiveSigners_needsAnArchiveAndTheWalletThatSignedIt(t *testing.T) {
	if _, err := remoteArchiveSigners(&Flags{OperatorWallet: "0xabc"}); err == nil {
		t.Error("--remote went ahead without an archive to verify")
	}
	if _, err := remoteArchiveSigners(&Flags{Archive: "/tmp/a.tar.gz"}); err == nil {
		t.Error("--remote went ahead with nobody to verify the archive against")
	}
	got, err := remoteArchiveSigners(&Flags{Archive: "/tmp/a.tar.gz", OperatorWallet: "0xabc"})
	if err != nil || len(got) != 1 || got[0] != "0xabc" {
		t.Errorf("got %v, %v", got, err)
	}
}

func TestBuildRemoteCommand_runsTheArchivesCLI(t *testing.T) {
	r := &RemoteOrchestrator{flags: &Flags{VpsIP: "1.2.3.4"}}
	r.node.User = "root"
	if cmd := r.buildRemoteCommand(); !strings.HasPrefix(cmd, installedArchiveCLI+" node install") {
		t.Errorf("the install does not run the uploaded CLI: %s", cmd)
	}
}
