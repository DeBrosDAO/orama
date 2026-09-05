package install

import (
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
		VpsIP:             "1.2.3.4",
		Domain:            "node1.example.com",
		BaseDomain:        "example.com",
		JoinAddress:       "https://node0.example.com",
		Token:             "tok",
		CAFingerprint:     "fp",
		SSHUser:           "ubuntu",
		Environment:       "devnet",
		OperatorWallet:    "0xabc",
		PeersStr:          "/ip4/10.0.0.1/tcp/4001/p2p/Qm",
		IPFSPeerID:        "QmPeer",
		IPFSAddrs:         "/ip4/10.0.0.1/tcp/4001",
		IPFSClusterPeerID: "QmCluster",
		IPFSClusterAddrs:  "/ip4/10.0.0.1/tcp/9096",
		ClusterSecret:     "cs",
		SwarmKey:          "sk",
		Nameserver:        true,
		Force:             true,
		SkipChecks:        true,
		SkipFirewall:      true,
		DryRun:            true,
		AnyoneClient:      true,
	}
}

func TestRemoteInstallArgs_forwardsEveryFlag(t *testing.T) {
	line := strings.Join(remoteInstallArgs(fullFlags()), " ")

	for _, want := range []string{
		"--vps-ip 1.2.3.4",
		"--domain node1.example.com",
		"--base-domain example.com",
		"--join https://node0.example.com",
		"--token tok",
		"--ca-fingerprint fp",
		"--ssh-user ubuntu",
		"--environment devnet",
		"--operator-wallet 0xabc",
		"--peers /ip4/10.0.0.1/tcp/4001/p2p/Qm",
		"--ipfs-peer QmPeer",
		"--ipfs-addrs /ip4/10.0.0.1/tcp/4001",
		"--ipfs-cluster-peer QmCluster",
		"--ipfs-cluster-addrs /ip4/10.0.0.1/tcp/9096",
		"--cluster-secret cs",
		"--swarm-key sk",
		"--nameserver",
		"--force",
		"--skip-checks",
		"--skip-firewall",
		"--dry-run",
		"--anyone-client",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("%q is not forwarded:\n%s", want, line)
		}
	}
}

// Every string field of Flags has to appear in the forwarded list, so adding a
// flag and forgetting to forward it is caught here rather than by a node that
// installs without it.
func TestRemoteInstallArgs_coversEveryStringField(t *testing.T) {
	line := strings.Join(remoteInstallArgs(fullFlags()), " ")

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
		if !strings.Contains(line, value) {
			t.Errorf("Flags.%s (%q) is never forwarded to the node:\n%s", field.Name, value, line)
		}
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
