package provision

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/sshx"
	"golang.org/x/crypto/ssh"
	"gopkg.in/yaml.v3"
)

func TestNewServerHostKey_userDataInstallsTheKey(t *testing.T) {
	hk, err := newServerHostKey("e2e-testrun1-n1")
	if err != nil {
		t.Fatal(err)
	}
	body, ok := strings.CutPrefix(hk.userData, cloudConfigHeader)
	if !ok {
		t.Fatalf("user data does not start with %q", cloudConfigHeader)
	}
	var cc hostKeyCloudConfig
	if err := yaml.Unmarshal([]byte(body), &cc); err != nil {
		t.Fatalf("user data is not YAML: %v", err)
	}
	if cc.SSHDeleteKeys || !strings.Contains(body, "ssh_deletekeys: false") {
		t.Fatal("cloud-init would regenerate the host keys")
	}
	signer, err := ssh.ParsePrivateKey([]byte(cc.SSHKeys["ed25519_private"]))
	if err != nil {
		t.Fatalf("the private host key does not parse: %v", err)
	}
	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(cc.SSHKeys["ed25519_public"]))
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []ssh.PublicKey{signer.PublicKey(), pub} {
		if !bytes.Equal(k.Marshal(), hk.pub.Marshal()) || k.Type() != ssh.KeyAlgoED25519 {
			t.Fatalf("the user data installs %s, not the pinned %s", sshx.Fingerprint(k), sshx.Fingerprint(hk.pub))
		}
	}
	other, _ := newServerHostKey("e2e-testrun1-n2")
	if bytes.Equal(other.pub.Marshal(), hk.pub.Marshal()) {
		t.Fatal("two servers got the same host key")
	}
}

func TestUp_pinsTheGeneratedHostKeys(t *testing.T) {
	e, st := upForTest(t)
	raw, err := os.ReadFile(st.KnownHostsFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range st.Nodes {
		key, err := e.cloud.hostKeyOf(n.PublicIP)
		if err != nil {
			t.Fatal(err)
		}
		line := n.PublicIP + " " + strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
		if !strings.Contains(string(raw), line) {
			t.Fatalf("%s is not pinned to the key its user data installed", n.Name)
		}
	}
}

func TestUp_foreignHostKeyIsRefused(t *testing.T) {
	e := newTestEnv(t)
	e.remote.foreignKey = true
	_, err := up(context.Background(), e.cfg, &testLogger{}, e.d)
	if err == nil || !strings.Contains(err.Error(), "phase host-keys") || !strings.Contains(err.Error(), "not the pinned") {
		t.Fatalf("up with a server presenting another host key: %v", err)
	}
	assertUpCleanedUp(t, e)
}

func TestAddExtra_foreignHostKeyDeletesTheServer(t *testing.T) {
	e, st := upForTest(t)
	before, _, _ := e.cloud.counts()
	e.remote.foreignKey = true
	if _, err := addExtra(context.Background(), st, "extra-1", "nbg1", testLimits, e.d); err == nil || !strings.Contains(err.Error(), "not the pinned") {
		t.Fatalf("addExtra with a foreign host key: %v", err)
	}
	if after, _, _ := e.cloud.counts(); after != before {
		t.Fatalf("servers %d -> %d: the refused extra leaked", before, after)
	}
	if len(st.Extras) != 0 {
		t.Fatal("the refused extra joined the state")
	}
}

func TestAddExtra_honorsTheRunLimits(t *testing.T) {
	e, st := upForTest(t)
	lim := extraLimits{serverType: DefaultServerType, serverLimit: 4, ttl: 90 * time.Minute}
	n, err := addExtra(context.Background(), st, "extra-1", "nbg1", lim, e.d)
	if err != nil {
		t.Fatal(err)
	}
	if got := e.cloud.capacityLimits[len(e.cloud.capacityLimits)-1]; got != 4 {
		t.Fatalf("capacity checked against %d, want the run's 4", got)
	}
	servers, _ := e.cloud.ListServers(context.Background(), "")
	for _, s := range servers {
		if s.ID == n.ServerID && s.Labels["e2e-ttl"] != lim.ttl.String() {
			t.Fatalf("extra labelled ttl %q, want %s", s.Labels["e2e-ttl"], lim.ttl)
		}
	}
}

func TestUp_sshRuleLimitedToTheRunner(t *testing.T) {
	e := newTestEnv(t)
	if _, err := up(context.Background(), e.cfg, &testLogger{}, e.d); err != nil {
		t.Fatal(err)
	}
	for _, rules := range e.cloud.firewallRules {
		for _, r := range rules {
			want := anywhereCIDRs
			if r.Port == "22" {
				want = strings.Join(e.cfg.RunnerCIDRs, ",")
			}
			if got := strings.Join(r.SourceIPs, ","); got != want {
				t.Fatalf("rule %s/%s open to %s, want %s", r.Protocol, r.Port, got, want)
			}
		}
	}
}

func TestRegisterAccess_logsSSHOpenToAnywhere(t *testing.T) {
	e := newTestEnv(t)
	e.cfg.RunnerCIDRs = splitList(anywhereCIDRs)
	log := &testLogger{}
	if _, err := up(context.Background(), e.cfg, log, e.d); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(log.lines, "\n"), EnvRunnerCIDR) {
		t.Fatal("an SSH rule open to anywhere was not logged")
	}
}

func TestUp_refusesARunIDWithDNSRecords(t *testing.T) {
	e := newTestEnv(t)
	e.dns.add("NS", "e2e-testrun1."+testZone, "ns1.e2e-testrun1."+testZone, time.Now())
	_, err := up(context.Background(), e.cfg, &testLogger{}, e.d)
	if err == nil || !strings.Contains(err.Error(), "already has 1 DNS records") {
		t.Fatalf("up with records under its subdomain: %v", err)
	}
	if len(e.dns.records) != 1 {
		t.Fatal("the refused Up deleted the other run's records")
	}
	if s, _, _ := e.cloud.counts(); s != 0 || len(e.cmd.lines()) != 0 {
		t.Fatal("something was created or run before preflight refused")
	}
}

func TestUp_delegationRetriesAnUnreachableNode(t *testing.T) {
	e := newTestEnv(t)
	e.cmd.flaky = map[string][]int{"node dns delegation": {cliExitUnavailable, cliExitUnavailable}}
	if _, err := up(context.Background(), e.cfg, &testLogger{}, e.d); err != nil {
		t.Fatalf("up with two transient delegation failures: %v", err)
	}
}

func TestUp_delegationGivesUpAfterBoundedRetries(t *testing.T) {
	e := newTestEnv(t)
	codes := make([]int, maxUnavailableInARow+1)
	for i := range codes {
		codes[i] = cliExitUnavailable
	}
	e.cmd.flaky = map[string][]int{"node dns delegation": codes}
	_, err := up(context.Background(), e.cfg, &testLogger{}, e.d)
	if err == nil || !strings.Contains(err.Error(), "scripted flake") {
		t.Fatalf("up with the node unreachable past the retries: %v", err)
	}
	assertUpCleanedUp(t, e)
}

func TestUp_delegationAuthErrorFailsAtOnce(t *testing.T) {
	e := newTestEnv(t)
	e.cmd.flaky = map[string][]int{"node dns delegation": {3}}
	_, err := up(context.Background(), e.cfg, &testLogger{}, e.d)
	if err == nil || !strings.Contains(err.Error(), "exit 3") {
		t.Fatalf("up with an auth failure reading the slots: %v", err)
	}
	assertUpCleanedUp(t, e)
}
