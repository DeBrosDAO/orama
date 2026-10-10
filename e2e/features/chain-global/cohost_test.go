//go:build e2e_fleet

package chainglobal

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/monitor"
)

// Paths the chain unit must not see (e2e/scripts/chain-deploy.sh write_unit
// InaccessiblePaths): the cluster's WireGuard keys and its config.
var hiddenFromChain = []string{"/etc/wireguard", "/etc/orama"}

// maxBlockAgeSec is how old the latest block may be on a healthy chain: many
// block intervals, far less than one e2e epoch.
const maxBlockAgeSec = 60

// unitProps reads systemd properties of the chain unit on n.
func unitProps(t *testing.T, c *chain.Chain, n fleet.Node, props ...string) map[string]string {
	t.Helper()
	out := c.F.MustExec(t, n, "systemctl show "+chain.Unit+" -p "+strings.Join(props, " -p "))
	got := map[string]string{}
	for _, line := range strings.Split(out.Stdout, "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			got[k] = v
		}
	}
	return got
}

// TestCoHost_chainUnitRunsConfined: on every node the chain unit is active as
// the orama-chain user, ProtectSystem=strict, NoNewPrivileges, and inside its
// mount namespace /etc/wireguard and /etc/orama are empty although both hold
// files on the host: the validator process cannot read the cluster's keys.
func TestCoHost_chainUnitRunsConfined(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	for _, n := range c.Nodes() {
		p := unitProps(t, c, n, "ActiveState", "User", "ProtectSystem", "NoNewPrivileges", "MainPID", "InaccessiblePaths", "IPAddressDeny")
		if p["ActiveState"] != "active" || p["User"] != chain.ServiceUser || p["ProtectSystem"] != "strict" || p["NoNewPrivileges"] != "yes" {
			t.Errorf("%s: %s %v", n.Name, chain.Unit, p)
		}
		for _, path := range hiddenFromChain {
			if !strings.Contains(p["InaccessiblePaths"], path) {
				t.Errorf("%s: InaccessiblePaths %q lacks %s", n.Name, p["InaccessiblePaths"], path)
			}
			requireHiddenInUnit(t, c, n, p["MainPID"], path)
		}
		if p["IPAddressDeny"] == "" {
			t.Errorf("%s: the chain unit denies no address (IPAddressDeny empty)", n.Name)
		}
	}
}

// requireHiddenInUnit compares a directory's entry count on the host and in
// the unit's mount namespace.
func requireHiddenInUnit(t *testing.T, c *chain.Chain, n fleet.Node, pid, path string) {
	t.Helper()
	if _, err := strconv.Atoi(pid); err != nil || pid == "0" {
		t.Fatalf("%s: chain unit MainPID %q", n.Name, pid)
	}
	count := func(prefix string) int {
		out := c.F.Exec(t, n, prefix+"sh -c "+fleet.ShellQuote("ls -A "+path+" 2>/dev/null | wc -l"))
		v, _ := strconv.Atoi(strings.TrimSpace(out.Stdout))
		return v
	}
	host, inside := count(""), count("nsenter -t "+pid+" -m -- ")
	if host == 0 {
		t.Fatalf("%s: %s is empty on the host too; the check proves nothing", n.Name, path)
	}
	if inside != 0 {
		t.Errorf("%s: the chain unit sees %d entries in %s", n.Name, inside, path)
	}
}

// TestCoHost_stateOwnedByTheChainUser: the chain home is orama-chain's, mode
// 0700; the consensus key inside is 0600; the binary is root's, not
// writable by the chain user (docs/whitepaper/technical-reference/vol2/39-chain-architecture.md "The stagenet deploy script").
func TestCoHost_stateOwnedByTheChainUser(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	for _, n := range c.Nodes() {
		want := map[string]string{
			chain.Home: chain.ServiceUser + " 700",
			chain.Home + "/config/priv_validator_key.json": chain.ServiceUser + " 600",
			chain.Oramad: "root 755",
		}
		for path, w := range want {
			out := c.F.MustExec(t, n, "stat -c '%U %a' "+fleet.ShellQuote(path))
			if got := strings.TrimSpace(out.Stdout); got != w {
				t.Errorf("%s: %s is %q, want %q", n.Name, path, got, w)
			}
		}
	}
}

// TestCoHost_p2pOnWireGuardRPCOnLoopback: p2p (31000) listens only on the
// node's WireGuard address; RPC, gRPC, REST and Prometheus (31001-31004)
// only on loopback; nothing of the chain listens on every interface; the
// firewall opens none of those ports to the internet (docs/whitepaper/technical-reference/vol2/37-global-nodes.md: "p2p
// public, RPC, gRPC, REST and Prometheus on loopback"; the run keeps p2p on
// the overlay).
func TestCoHost_p2pOnWireGuardRPCOnLoopback(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	if c.F.State.IsStagenet() {
		harness.SkipNotApplicable(t, "the stagenet chain runs in the orama-global netns and answers on "+c.Host()+", not on the host's loopback: its ports are not host listeners")
	}
	for _, n := range c.Nodes() {
		seen := map[int]bool{}
		for _, l := range c.F.Listeners(t, n) {
			if l.Port < chain.P2PPort || l.Port > chain.PromPort {
				continue
			}
			seen[l.Port] = true
			want := "127.0.0.1"
			if l.Port == chain.P2PPort {
				want = n.WGIP
			}
			if l.Addr != want || l.Public() {
				t.Errorf("%s: port %d listens on %s, want only %s", n.Name, l.Port, l.Addr, want)
			}
		}
		for _, p := range []int{chain.P2PPort, chain.RPCPort, chain.GRPCPort, chain.APIPort, chain.PromPort} {
			if !seen[p] {
				t.Errorf("%s: nothing listens on chain port %d", n.Name, p)
			}
		}
		fw := c.F.Firewall(t, n)
		for p := chain.P2PPort; p <= chain.PromPort; p++ {
			if fw.Allows(fmt.Sprintf("%d/tcp", p)) {
				t.Errorf("%s: ufw opens chain port %d/tcp to the internet", n.Name, p)
			}
		}
	}
}

// clusterPreferences is the node's preferences.yaml (core/pkg/install/preferences.go).
const clusterPreferences = "/opt/orama/.orama/preferences.yaml"

// TestCoHost_clusterNodeKeepsItsClusterRole: installing the chain beside a
// cluster node leaves the node's preferences on the cluster role, so
// orama-node keeps booting the cluster graph (website/src/docs/contributor/architecture-reference.mdx, node
// roles): the role is neither global nor both, and WireGuard, which only the
// cluster graph starts, is up.
func TestCoHost_clusterNodeKeepsItsClusterRole(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	for _, n := range c.Nodes() {
		prefs := c.F.MustExec(t, n, "cat "+clusterPreferences).Stdout
		for _, line := range strings.Split(prefs, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "role:") {
				t.Errorf("%s: %s records %q, want the cluster role (no role line)", n.Name, clusterPreferences, line)
			}
		}
		if s := c.F.Unit(t, n, "orama-node.service"); s != "active" {
			t.Errorf("%s: orama-node.service is %s", n.Name, s)
		}
	}
}

// TestCoHost_unitsAndMonitorView:the chain unit is among the node's
// orama-global units, and the operator's monitor sees each node's chain
// responsive, on the run's chain id, with both other validators as peers.
func TestCoHost_unitsAndMonitorView(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	for _, n := range c.Nodes() {
		out := c.F.MustExec(t, n, "systemctl list-units 'orama-global-*' --all --no-legend --plain")
		if !strings.Contains(out.Stdout, chain.Unit) {
			t.Errorf("%s: orama-global units %q do not include %s", n.Name, out.Stdout, chain.Unit)
		}
	}
	r := monitor.Fetch(t, harness.CLI(t), c.F.State.Env)
	for _, n := range r.Nodes {
		if n.Report == nil || n.Report.Chain == nil {
			t.Errorf("%s: the monitor report has no chain section", n.Host)
			continue
		}
		ch := n.Report.Chain
		if !ch.Responsive || ch.ChainID != c.ID || ch.Peers != len(c.Nodes())-1 || ch.BlockAgeSec > maxBlockAgeSec {
			t.Errorf("%s: monitor chain section %+v", n.Host, ch)
		}
	}
	res := infra.Run(t, harness.CLI(t), "monitor", "chain", "--env", c.F.State.Env)
	infra.ExpectExit(t, res, infra.ExitOK, c.ID)
}
