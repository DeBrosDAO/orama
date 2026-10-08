//go:build e2e_fleet

package tornetwork

import (
	"fmt"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/pkg/constants"
)

const (
	// gatePort is the loopback port the test's own gate listens on; it is in the
	// global block, away from the installed gate's 31022.
	gatePort = 31090
	// account is a well-formed Orama address no test funds.
	account = "orama1tehv5km5e9y706rc2gzk9yyun9dljjjny06dv2"
)

// startGate runs `orama global txgate` on n in a transient unit and returns
// its base URL on n's loopback. upstream is the chain REST API to forward to.
func startGate(t *testing.T, f *fleet.Fleet, n fleet.Node, upstream string) string {
	t.Helper()
	unit := "e2e-txgate-" + randomSuffix(t)
	listen := fmt.Sprintf("127.0.0.1:%d", gatePort)
	f.MustExec(t, n, fmt.Sprintf("systemd-run --quiet --unit %s -p User=nobody -p NoNewPrivileges=yes %s global txgate --listen %s --upstream %s",
		unit, infra.OramaBinOnNode, listen, upstream))
	t.Cleanup(func() {
		cleanup(t, f, n, "systemctl stop "+unit+" && systemctl reset-failed "+unit+" 2>/dev/null; true")
	})
	base := "http://" + listen
	eventually.Require(t, pollEvery, bootstrapBudget, "the tx gate on "+n.Name+" to listen", func() (bool, error) {
		out := f.Exec(t, n, fmt.Sprintf("curl -s -o /dev/null -w %%{http_code} %s/", base))
		return strings.TrimSpace(out.Stdout) == "404", nil
	})
	return base
}

// curl runs curl on n and returns the status and body.
func curl(t *testing.T, f *fleet.Fleet, n fleet.Node, args ...string) (int, string) {
	t.Helper()
	const mark = "\n__STATUS__"
	out := f.Exec(t, n, "curl -s -w "+fleet.ShellQuote(mark+"%{http_code}")+" "+strings.Join(args, " "))
	body, status, _ := strings.Cut(out.Stdout, mark)
	var code int
	fmt.Sscanf(strings.TrimSpace(status), "%d", &code)
	return code, body
}

// TestTxgate_servesOnlyTheWalletCalls: the gate behind the validator onion
// service forwards the account read, the broadcast and the transaction lookup,
// and answers every other path of the chain REST API with its own 404 (so the
// node's queries, validator list and transaction search are not reachable over
// the onion), a wrong method with 405 and an oversized or untyped broadcast
// with 413 and 415. With the chain behind it the allowed calls get the chain's
// answer; with a dead upstream they get 502 and no detail.
func TestTxgate_servesOnlyTheWalletCalls(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := f.State.Nodes[0]
	upstream := constants.LocalChainAPIURL()
	chainUp := f.State.ChainID != ""
	if !chainUp {
		upstream = "http://127.0.0.1:1" // nothing listens: the allowed calls reach a dead chain API
	}
	base := startGate(t, f, n, upstream)

	const notServed = "not served over the onion service"
	for _, path := range []string{
		"/status", "/cosmos/bank/v1beta1/balances/" + account, "/cosmos/staking/v1beta1/validators",
		"/cosmos/tx/v1beta1/txs?query=message.sender%3D%27x%27", "/cosmos/base/tendermint/v1beta1/node_info",
		"/cosmos/auth/v1beta1/accounts", "/cosmos/tx/v1beta1/txs/short",
	} {
		code, body := curl(t, f, n, base+path)
		if code != 404 || !strings.Contains(body, notServed) {
			t.Errorf("GET %s = %d %s, want the gate's 404", path, code, body)
		}
	}
	if code, _ := curl(t, f, n, "-X", "POST", "-H", "'Content-Type: application/json'", "-d", "'{}'", base+"/cosmos/auth/v1beta1/accounts/"+account); code != 405 {
		t.Errorf("POST to the account read = %d, want 405", code)
	}
	if code, _ := curl(t, f, n, base+"/cosmos/tx/v1beta1/txs"); code != 405 {
		t.Errorf("GET of the broadcast = %d, want 405", code)
	}
	if code, _ := curl(t, f, n, "-X", "POST", "-H", "'Content-Type: text/plain'", "-d", "x", base+"/cosmos/tx/v1beta1/txs"); code != 415 {
		t.Errorf("a broadcast that is not JSON = %d, want 415", code)
	}
	huge := "/tmp/e2e-txgate-huge-" + randomSuffix(t)
	f.MustExec(t, n, "head -c 600000 /dev/zero | tr '\\0' A > "+huge)
	t.Cleanup(func() { cleanup(t, f, n, "rm -f "+huge) })
	if code, _ := curl(t, f, n, "-X", "POST", "-H", "'Content-Type: application/json'", "--data-binary", "@"+huge, base+"/cosmos/tx/v1beta1/txs"); code != 413 {
		t.Errorf("a 600 kB broadcast = %d, want 413", code)
	}

	code, body := curl(t, f, n, base+"/cosmos/auth/v1beta1/accounts/"+account)
	if chainUp {
		if strings.Contains(body, notServed) || (code != 200 && code != 404) || !strings.Contains(body, `"`) {
			t.Errorf("the account read reached the chain as %d %s", code, body)
		}
		return
	}
	if code != 502 || strings.Contains(body, "127.0.0.1") || strings.Contains(body, "refused") {
		t.Errorf("a dead upstream = %d %s, want a bare 502", code, body)
	}
}

// TestTxgate_listenMustBeLoopback: the gate has no authentication of its own,
// so it refuses to listen on anything but a loopback address.
func TestTxgate_listenMustBeLoopback(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := f.State.Nodes[0]
	for _, listen := range []string{"0.0.0.0:31091", ":31091", n.PublicIP + ":31091"} {
		out := infra.OnNode(t, f, n, "global", "txgate", "--listen", listen, "--upstream", "http://127.0.0.1:1")
		infra.ExpectNodeExit(t, "txgate --listen "+listen, out, infra.ExitUsage, "loopback")
	}
	out := infra.OnNode(t, f, n, "global", "txgate", "--listen", "127.0.0.1:31091", "--upstream", "https://127.0.0.1:1")
	infra.ExpectNodeExit(t, "an https upstream", out, infra.ExitUsage, "http://host:port")
}

// TestInstall_torRolesAreRefusedBeforeTheMachineChanges: the install
// mistakes the Tor roles add (exit without relay, a dirauth beside a relay, an
// onion service with no network file, Tor options on a service that has no relay,
// no public address, no authority keys) are refused with the usage code or
// the failure code before anything on the machine changes.
func TestInstall_torRolesAreRefusedBeforeTheMachineChanges(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := f.State.Nodes[0]
	const staged = "/e2e-no-such-staged-dir"
	before := f.MustExec(t, n, "ls -A /etc/systemd/system | grep -c orama-global; ls -A /usr/lib/orama-global 2>/dev/null | wc -l").Stdout
	usage := []struct {
		name, want string
		args       []string
	}{
		{"exit without relay", "use --services relay,exit", []string{"--services", "chain,exit"}},
		{"a dirauth beside a relay", "not both", []string{"--services", "dirauth,relay"}},
	}
	for _, c := range usage {
		infra.ExpectNodeExit(t, c.name, infra.OnNode(t, f, n, append([]string{"global", "install"}, c.args...)...), infra.ExitUsage, c.want)
	}
	failure := []struct {
		name, want string
		args       []string
	}{
		{"a relay with no node id", "--tor-node-id", []string{"--services", "relay", "--staged-dir", staged, "--tor-address", "192.5.5.241", "--tor-contact", "ops@example.org"}},
		{"a relay with no public address", "--tor-address and --tor-contact", []string{"--services", "relay", "--staged-dir", staged, "--tor-node-id", "n"}},
		{"a dirauth with no key bundle", "--tor-authority-keys", []string{"--services", "dirauth", "--staged-dir", staged, "--tor-address", "192.5.5.241", "--tor-contact", "ops@example.org"}},
		{"Tor options on a chain", "apply only with the relay, dirauth or onion service", []string{"--services", "chain", "--staged-dir", staged, "--tor-address", "192.5.5.241"}},
		{"an onion service with no network file", constants.TorNetworkFile, []string{"--services", "onion", "--staged-dir", staged}},
		{"a relay with no network file", constants.TorNetworkFile, []string{"--services", "relay", "--staged-dir", staged, "--tor-address", "192.5.5.241", "--tor-contact", "ops@example.org", "--tor-node-id", "n"}},
	}
	for _, c := range failure {
		infra.ExpectNodeExit(t, c.name, infra.OnNode(t, f, n, append([]string{"global", "install"}, c.args...)...), infra.ExitFailure, c.want)
	}
	if after := f.MustExec(t, n, "ls -A /etc/systemd/system | grep -c orama-global; ls -A /usr/lib/orama-global 2>/dev/null | wc -l").Stdout; after != before {
		t.Fatalf("a refused install changed %s: %q -> %q", n.Name, before, after)
	}
}
