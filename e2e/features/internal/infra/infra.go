//go:build e2e_fleet

// Package infra holds what the infrastructure features (bootstrap, install,
// invite-join, wireguard-firewall, rqlite-raft, boot-lifecycle,
// rollout-upgrade) share: the CLI's exit codes, the node layout they audit,
// and the cluster-level waits every destructive test ends with.
//
// Every value here is read from core, cited where it is defined, so a test
// that asserts it asserts what the code ships.
package infra

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
)

// Exit codes of the orama CLI (core/cmd/orama/internal/clierr, e2e/README.md).
const (
	ExitOK          = 0
	ExitFailure     = 1
	ExitUsage       = 2
	ExitAuth        = 3
	ExitNotFound    = 4
	ExitUnavailable = 5
	ExitConflict    = 6
	ExitAborted     = 7
)

// Units every core node runs. orama-node.service is the only host unit the
// installer writes; everything else is a template instance it supervises
// (core/pkg/namespace/index.go IndexSupervisor, core/systemd).
const (
	NodeUnit         = "orama-node.service"
	IndexRQLiteUnit  = "orama-namespace-rqlite@index.service"
	IndexOlricUnit   = "orama-namespace-olric@index.service"
	IndexGatewayUnit = "orama-namespace-gateway@index.service"
	WireGuardUnit    = "orama-namespace-wireguard@index.service"
	// LeftoverWireGuardUnit is disabled by install and never stopped: stopping
	// it would run wg-quick down (core/pkg/systemd/manager.go).
	LeftoverWireGuardUnit = "wg-quick@wg0.service"
	CoreDNSUnit           = "orama-namespace-coredns@nameserver.service"
	ChainUnit             = "orama-global-chain.service" // core/pkg/constants/chain.go ChainServiceUnit
	PrivhelperSocket      = "orama-privhelper.socket"
	IndexNamespace        = "index"
	WireGuardIface        = "wg0"
	WireGuardSubnet       = "10.0.0.0/24"
	WireGuardPort         = 51820
	IndexRQLiteHTTP       = 10100 // core/pkg/constants/ports.go RQLiteHTTPPort
	IndexRQLiteRaft       = 10101 // core/pkg/constants/ports.go RQLiteRaftPort
	ChainP2PPort          = 31000 // e2e/scripts/chain-deploy.sh P2P_PORT, bound to the WG IP
	ChainRPCPort          = 31001 // loopback only
	OramaBinOnNode        = "orama"
	WireGuardConfPath     = "/etc/wireguard/wg0.conf"
)

// Node layout (core/pkg/install/paths.go, core/pkg/archivetrust/anchor.go).
const (
	OramaBase        = "/opt/orama"
	OramaBinDir      = "/opt/orama/bin"
	OramaDir         = "/opt/orama/.orama"
	OramaSecretsDir  = "/opt/orama/.orama/secrets"
	OramaConfigsDir  = "/opt/orama/.orama/configs"
	NodeConfigPath   = "/opt/orama/.orama/configs/node.yaml"
	CoreRQLiteDir    = "/opt/orama/.orama/data/rqlite"
	ArchiveSigners   = "/etc/orama/archive-signers"
	StagedManifest   = "/opt/orama/manifest.json"
	StagedSignature  = "/opt/orama/manifest.sig"
	PrivhelperSock   = "/run/orama-privhelper.sock"
	IPv6SysctlConf   = "/etc/sysctl.d/99-orama-disable-ipv6.conf"
	RAMHygieneConf   = "/etc/sysctl.d/99-orama-ram-hygiene.conf"
	CoredumpConf     = "/etc/systemd/coredump.conf.d/orama.conf"
	NodeKeyPath      = "/opt/orama/.orama/secrets/node-key.pem"
	RQLiteAuthSecret = "/opt/orama/.orama/secrets/rqlite-auth.json"
)

// Budgets shared by the infrastructure features.
const (
	// PollEvery paces every wait on the cluster.
	PollEvery = 5 * time.Second
	// ConvergeBudget: a node restart re-runs the boot readiness gate and the
	// monitor telemetry is gathered every 10s (docs/MONITORING.md); the
	// lifecycle harness gives a reconverge ten minutes.
	ConvergeBudget = 10 * time.Minute
	// ColdStartBudget: every node restarted at once elects a leader again.
	ColdStartBudget = 15 * time.Minute
	// ServingBudget: DNS and the gateways serve before raft settles.
	ServingBudget = 5 * time.Minute
	// UpgradeBudget bounds one CLI-driven upgrade or rollout of the fleet.
	UpgradeBudget = 60 * time.Minute
	// InstallBudget bounds one `orama node setup` of a fresh server.
	InstallBudget = 30 * time.Minute
	dialBudget    = 15 * time.Second
	httpsPort     = "443"
)

// OnNode runs `orama <args>` on n as root, the way an operator logged into the
// node does for the local commands (docs/CLI_REFERENCE.md "orama node": install,
// stop, start, restart, status, report, invite, schema, stage-archive run on
// the node itself). Arguments are shell-quoted; the exit code is returned.
func OnNode(t testing.TB, f *fleet.Fleet, n fleet.Node, args ...string) fleet.Output {
	t.Helper()
	return f.Exec(t, n, OramaCommand(args...))
}

// OramaCommand is the shell form of `orama <args>`.
func OramaCommand(args ...string) string {
	q := make([]string, 0, len(args)+1)
	q = append(q, OramaBinOnNode)
	for _, a := range args {
		q = append(q, fleet.ShellQuote(a))
	}
	return strings.Join(q, " ")
}

// Run runs the CLI under test and fails the test only when it could not run.
func Run(t testing.TB, cli *oramacli.Runner, args ...string) oramacli.Result {
	t.Helper()
	res, err := cli.For(t).Run(t.Context(), args...)
	if err != nil {
		t.Fatal(cli.Recorder.Redactor().Redact(err.Error()))
	}
	return res
}

// RunFor is Run under an explicit budget, for commands that outlive the CLI's
// default (a rolling upgrade, an install).
func RunFor(t testing.TB, cli *oramacli.Runner, budget time.Duration, args ...string) oramacli.Result {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), budget)
	defer cancel()
	res, err := cli.For(t).Run(ctx, args...)
	if err != nil {
		t.Fatal(cli.Recorder.Redactor().Redact(err.Error()))
	}
	return res
}

// ExpectExit fails unless res exited with want and its combined output
// contains every one of the fragments.
func ExpectExit(t testing.TB, res oramacli.Result, want int, fragments ...string) {
	t.Helper()
	out := res.Stdout + res.Stderr
	if res.Exit != want {
		t.Fatalf("orama %s: exit %d, want %d\n%s", strings.Join(oramacli.RedactArgs(res.Args), " "), res.Exit, want, out)
	}
	for _, frag := range fragments {
		if !strings.Contains(out, frag) {
			t.Errorf("orama %s: output lacks %q\n%s", strings.Join(oramacli.RedactArgs(res.Args), " "), frag, out)
		}
	}
}

// ExpectRefused fails when res succeeded, or when its output lacks a fragment.
func ExpectRefused(t testing.TB, res oramacli.Result, fragments ...string) {
	t.Helper()
	out := res.Stdout + res.Stderr
	if res.Exit == ExitOK {
		t.Fatalf("orama %s succeeded, want a refusal\n%s", strings.Join(oramacli.RedactArgs(res.Args), " "), out)
	}
	for _, frag := range fragments {
		if !strings.Contains(out, frag) {
			t.Errorf("orama %s: refusal lacks %q\n%s", strings.Join(oramacli.RedactArgs(res.Args), " "), frag, out)
		}
	}
}

// Stat is a file's owner, group and octal mode on a node.
type Stat struct {
	Owner, Group, Mode, Type string
}

// StatFile stats path on n (no-follow). Missing is reported as ok=false.
func StatFile(t testing.TB, f *fleet.Fleet, n fleet.Node, path string) (Stat, bool) {
	t.Helper()
	out := f.Exec(t, n, "stat -c '%U %G %a %F' -- "+fleet.ShellQuote(path))
	if out.Exit != 0 {
		return Stat{}, false
	}
	fields := strings.Fields(out.Stdout)
	if len(fields) < 4 {
		t.Fatalf("%s: unexpected stat output %q for %s", n.Name, out.Stdout, path)
	}
	return Stat{Owner: fields[0], Group: fields[1], Mode: fields[2], Type: strings.Join(fields[3:], " ")}, true
}

// RequireStat fails unless path exists on n with owner:group and mode.
func RequireStat(t testing.TB, f *fleet.Fleet, n fleet.Node, path, owner, group, mode string) {
	t.Helper()
	st, ok := StatFile(t, f, n, path)
	if !ok {
		t.Errorf("%s: %s does not exist", n.Name, path)
		return
	}
	if st.Owner != owner || st.Group != group || st.Mode != mode {
		t.Errorf("%s: %s is %s:%s %s, want %s:%s %s", n.Name, path, st.Owner, st.Group, st.Mode, owner, group, mode)
	}
}

// NodeByHost finds the fleet member a report names (public or WG address).
func NodeByHost(f *fleet.Fleet, host string) (fleet.Node, error) {
	if n, ok := f.Lookup(host); ok {
		return n, nil
	}
	return fleet.Node{}, fmt.Errorf("%q is no member of run %s", host, f.State.RunID)
}
