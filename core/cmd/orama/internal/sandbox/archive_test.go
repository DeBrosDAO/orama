package sandbox

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/inspector"
)

const testWallet = "0x1111111111111111111111111111111111111111"

func testSandbox(t *testing.T) *SandboxState {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	return &SandboxState{
		Name: "sbx-test",
		Servers: []ServerState{
			{Name: "sbx-test-1", IP: "203.0.113.1", Role: "nameserver"},
			{Name: "sbx-test-2", IP: "203.0.113.2", Role: "nameserver"},
			{Name: "sbx-test-3", IP: "203.0.113.3", Role: "node"},
		},
	}
}

// pinKeys writes the sandbox's known_hosts as pinHostKeys would.
func pinKeys(t *testing.T, state *SandboxState) string {
	t.Helper()
	path, err := knownHostsPath(state.Name)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("203.0.113.1 ssh-ed25519 AAAA\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// checkPinned fails unless every node is a sandbox server reached as root with
// the sandbox key and only the pinned host keys.
func checkPinned(t *testing.T, state *SandboxState, nodes []inspector.Node, knownHosts string) {
	t.Helper()
	if len(nodes) != len(state.Servers) {
		t.Fatalf("got %d nodes, want every server (%d)", len(nodes), len(state.Servers))
	}
	for i, n := range nodes {
		if n.Host != state.Servers[i].IP || n.User != "root" || n.SSHKey != "/key" || n.KnownHostsFile != knownHosts {
			t.Fatalf("node %d = %+v, want %s as root with /key, pinned to %s", i, n, state.Servers[i].IP, knownHosts)
		}
	}
}

// Create puts the archive on every server through node setup's verified path
// (verify here, canonical upload, node-side stage-archive), trusting exactly
// the operator's wallet — never a tar extraction of its own.
func TestInstallArchive_usesTheVerifiedPathWithTheOperatorWallet(t *testing.T) {
	state := testSandbox(t)
	knownHosts := pinKeys(t, state)

	var gotNodes []inspector.Node
	var gotArchive string
	var gotTrusted []string
	orig := ensureArchives
	t.Cleanup(func() { ensureArchives = orig })
	ensureArchives = func(nodes []inspector.Node, archive string, trusted []string) error {
		gotNodes, gotArchive, gotTrusted = nodes, archive, trusted
		return nil
	}

	nodes, err := pinnedNodes(state, "/key")
	if err != nil {
		t.Fatal(err)
	}
	if err := installArchive(nodes, "/tmp/orama.tar.gz", testWallet); err != nil {
		t.Fatal(err)
	}
	checkPinned(t, state, gotNodes, knownHosts)
	if gotArchive != "/tmp/orama.tar.gz" {
		t.Fatalf("archive = %q", gotArchive)
	}
	if len(gotTrusted) != 1 || gotTrusted[0] != testWallet {
		t.Fatalf("trusted = %v, want only the operator wallet", gotTrusted)
	}
}

func TestInstallArchive_failureStopsTheCreate(t *testing.T) {
	state := testSandbox(t)
	pinKeys(t, state)
	orig := ensureArchives
	t.Cleanup(func() { ensureArchives = orig })
	refused := errors.New("signed by someone else")
	ensureArchives = func([]inspector.Node, string, []string) error { return refused }

	nodes, err := pinnedNodes(state, "/key")
	if err != nil {
		t.Fatal(err)
	}
	if err := installArchive(nodes, "/tmp/orama.tar.gz", testWallet); !errors.Is(err, refused) {
		t.Fatalf("installArchive = %v, want the verification error", err)
	}
}

// Rollout goes through push: each node stages the archive with its installed
// orama against its own anchor, and no anchor is created or changed.
func TestPushToSandbox_usesPushOnEveryPinnedNode(t *testing.T) {
	state := testSandbox(t)
	knownHosts := pinKeys(t, state)

	called := false
	orig := pushArchive
	t.Cleanup(func() { pushArchive = orig })
	pushArchive = func(archive string, nodes []inspector.Node, direct bool, trust []string) error {
		called = true
		checkPinned(t, state, nodes, knownHosts)
		if archive != "/tmp/orama.tar.gz" || direct || trust != nil {
			t.Fatalf("push(%q, direct=%v, trust=%v), want a fanout that trusts only the nodes' anchors", archive, direct, trust)
		}
		return nil
	}

	nodes, err := pinnedNodes(state, "/key")
	if err != nil {
		t.Fatal(err)
	}
	if err := pushToSandbox(nodes, "/tmp/orama.tar.gz"); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("rollout did not push")
	}
}

// A sandbox from before host keys were pinned is refused, not reached with no
// host key check.
func TestPinnedNodes_refusesASandboxWithoutPinnedKeys(t *testing.T) {
	state := testSandbox(t)
	_, err := pinnedNodes(state, "/key")
	if err == nil || !strings.Contains(err.Error(), "no pinned SSH host keys") {
		t.Fatalf("pinnedNodes without pinned keys = %v", err)
	}
}

func TestPinnedNodes_emptySandbox(t *testing.T) {
	state := testSandbox(t)
	state.Servers = nil
	pinKeys(t, state)
	nodes, err := pinnedNodes(state, "/key")
	if err != nil || len(nodes) != 0 {
		t.Fatalf("pinnedNodes of no servers = %v, %v", nodes, err)
	}
}

// The genesis install makes the operator's wallet the archive trust anchor;
// without --operator-wallet it refuses to run.
func TestGenesisInstallCommand_passesTheOperatorWallet(t *testing.T) {
	cfg := &Config{Domain: "sbx.example.com"}
	cmd := genesisInstallCommand(cfg, ServerState{IP: "203.0.113.1", Role: "nameserver"}, testWallet)
	for _, want := range []string{
		"orama node install",
		"--operator-wallet '" + testWallet + "'",
		"--vps-ip '203.0.113.1'",
		"--nameserver",
		"--domain 'sbx.example.com'",
		"--skip-checks",
	} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("genesis install lacks %s: %s", want, cmd)
		}
	}
	if strings.Contains(cmd, "--token") || strings.Contains(cmd, "--join") {
		t.Fatalf("genesis install joins something: %s", cmd)
	}
}

// A join expects the operator's wallet as the cluster's signer and joins with
// the invite alone, which pins the genesis node's certificate. It used to pass
// --join http://<genesis>, which would override that pin with plain HTTP.
func TestJoinInstallCommand_expectsTheWalletAndPinsThroughTheInvite(t *testing.T) {
	cfg := &Config{Domain: "sbx.example.com"}
	cmd := joinInstallCommand(cfg, ServerState{IP: "203.0.113.3", Role: "node"}, testWallet, "orama1_abc")
	for _, want := range []string{
		"--operator-wallet '" + testWallet + "'",
		"--expect-archive-signers '" + testWallet + "'",
		"--token 'orama1_abc'",
		"--base-domain 'sbx.example.com'",
		"--skip-checks",
	} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("join install lacks %s: %s", want, cmd)
		}
	}
	if strings.Contains(cmd, "--join") || strings.Contains(cmd, "--nameserver") {
		t.Fatalf("join install of a plain node: %s", cmd)
	}
}

// Destroying a sandbox removes its pinned host keys with its state: the
// addresses go back to Hetzner.
func TestDeleteState_removesThePinnedHostKeys(t *testing.T) {
	state := testSandbox(t)
	if err := SaveState(state); err != nil {
		t.Fatal(err)
	}
	knownHosts := pinKeys(t, state)
	if err := DeleteState(state.Name); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(knownHosts); !os.IsNotExist(err) {
		t.Fatalf("pinned host keys survive the sandbox: %v", err)
	}
}

// Sandbox nodes register under the sandbox environment, which is how `orama`
// finds them with --env sandbox; the install is what records it.
func TestInstallCommands_registerTheSandboxEnvironmentOnStaging(t *testing.T) {
	cfg := &Config{Domain: "sbx.example.com"}
	srv := ServerState{IP: "203.0.113.3", Role: "node"}
	for _, cmd := range []string{
		genesisInstallCommand(cfg, srv, testWallet),
		joinInstallCommand(cfg, srv, testWallet, "orama1_abc"),
	} {
		if !strings.Contains(cmd, "--environment 'sandbox'") {
			t.Fatalf("install does not register the sandbox environment: %s", cmd)
		}
		if !strings.Contains(cmd, "--acme-ca 'letsencrypt-staging'") {
			t.Fatalf("install does not use Let's Encrypt staging: %s", cmd)
		}
	}
}

func TestWaitForCertificate_returnsOnceACertificateIsServed(t *testing.T) {
	srv := httptest.NewTLSServer(http.NotFoundHandler())
	defer srv.Close()
	if err := waitForCertificate(srv.Listener.Addr().String(), "sbx.example.com", time.Second); err != nil {
		t.Fatalf("waitForCertificate on a TLS listener: %v", err)
	}
}

func TestWaitForCertificate_timesOutWithoutOne(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	err = waitForCertificate(addr, "sbx.example.com", 0)
	if err == nil || !strings.Contains(err.Error(), "no certificate for sbx.example.com") {
		t.Fatalf("waitForCertificate with nothing listening = %v", err)
	}
}

// A name becomes a path under ~/.orama/sandboxes and part of each server's
// hostname, so nothing that could leave that directory or break a hostname
// is accepted.
func TestValidateName(t *testing.T) {
	for _, ok := range []string{"swift-falcon", "a", "feature-123"} {
		if err := validateName(ok); err != nil {
			t.Errorf("validateName(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", "../x", "a/b", "Upper", "-lead", "trail-", "with space", strings.Repeat("a", 41)} {
		if err := validateName(bad); err == nil {
			t.Errorf("validateName(%q) accepted", bad)
		}
	}
	if _, err := knownHostsPath("../../etc"); err == nil {
		t.Error("knownHostsPath accepted a name outside the sandboxes directory")
	}
}

// The host keys every server presents are pinned, in server order, in the
// sandbox's own known_hosts, and the nodes built from it check them strictly.
func TestPinHostKeys_pinsWhatEachServerPresents(t *testing.T) {
	state := testSandbox(t)
	orig := scanHostKeys
	t.Cleanup(func() { scanHostKeys = orig })
	scanHostKeys = func(ip string) ([]string, error) {
		return []string{ip + " ssh-ed25519 AAAA" + ip}, nil
	}

	if err := pinHostKeys(state, time.Second); err != nil {
		t.Fatal(err)
	}
	path, err := knownHostsPath(state.Name)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := ""
	for _, srv := range state.Servers {
		want += srv.IP + " ssh-ed25519 AAAA" + srv.IP + "\n"
	}
	if string(data) != want {
		t.Fatalf("known_hosts = %q, want %q", data, want)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("known_hosts mode: %v, %v", info.Mode(), err)
	}
	nodes, err := pinnedNodes(state, "/key")
	if err != nil {
		t.Fatal(err)
	}
	checkPinned(t, state, nodes, path)
	if opts := strings.Join(nodes[0].HostKeyOptions(), " "); !strings.Contains(opts, "StrictHostKeyChecking=yes") {
		t.Fatalf("pinned nodes do not check host keys strictly: %s", opts)
	}
}

// A server whose sshd never answers fails the create; nothing is pinned.
func TestPinHostKeys_serverThatNeverAnswers(t *testing.T) {
	state := testSandbox(t)
	orig := scanHostKeys
	t.Cleanup(func() { scanHostKeys = orig })
	scanHostKeys = func(ip string) ([]string, error) {
		if ip == state.Servers[1].IP {
			return nil, errors.New("connection refused")
		}
		return []string{ip + " ssh-ed25519 AAAA"}, nil
	}

	err := pinHostKeys(state, 0)
	if err == nil || !strings.Contains(err.Error(), state.Servers[1].Name) {
		t.Fatalf("pinHostKeys with a silent server = %v", err)
	}
	if _, err := pinnedNodes(state, "/key"); err == nil {
		t.Fatal("a failed pin left a known_hosts behind")
	}
}
