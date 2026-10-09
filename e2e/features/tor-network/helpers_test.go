//go:build e2e_fleet

package tornetwork

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/tornet"
)

const (
	// networkFile is the network file `orama global install` keeps on a node.
	networkFile = constants.GlobalStateRoot + "/" + constants.GlobalTorAuthoritiesFile

	// pollEvery paces every wait; consensusBudget is how long a freshly rolled-out
	// network may take to publish its first consensus (one voting interval and
	// the distribution delays).
	pollEvery       = 15 * time.Second
	consensusBudget = 35 * time.Minute
	bootstrapBudget = 15 * time.Minute
	fetchSeconds    = 120

	// clientPortBase is the first loopback SOCKS port of a throwaway client; each
	// client takes the next, so parallel tests on one node do not collide.
	clientPortBase = 19050
)

var clientPorts atomic.Int32

// roles are the nodes of the target that run each Tor role.
type roles struct {
	dirauth, relay, onion []fleet.Node
}

func (r roles) any() bool { return len(r.dirauth)+len(r.relay)+len(r.onion) > 0 }

// publishers are the nodes that serve the network a relay: authorities and relays.
func (r roles) publishers() []fleet.Node {
	return append(append([]fleet.Node{}, r.dirauth...), r.relay...)
}

// requireRoles finds the nodes that run a Tor role, and says the test does not
// apply when there are none.
func requireRoles(t *testing.T) (*fleet.Fleet, roles) {
	t.Helper()
	f := harness.Fleet(t)
	var r roles
	for _, n := range f.State.Nodes {
		if f.Unit(t, n, constants.GlobalTorDirauthUnit) == infra.UnitActive {
			r.dirauth = append(r.dirauth, n)
		}
		if f.Unit(t, n, constants.GlobalTorRelayUnit) == infra.UnitActive {
			r.relay = append(r.relay, n)
		}
		if f.Unit(t, n, constants.GlobalTorOnionUnit) == infra.UnitActive {
			r.onion = append(r.onion, n)
		}
	}
	if !r.any() {
		harness.SkipNotApplicable(t, "no node of this target runs a Tor role: install them with `orama global install --services dirauth|relay|onion` (docs/TOR_NETWORK.md, Rolling it out)")
	}
	return f, r
}

// networkOf reads the network file a node installed.
func networkOf(t *testing.T, f *fleet.Fleet, n fleet.Node) tornet.Network {
	t.Helper()
	network, err := tornet.ParseNetwork(f.ReadFile(t, n, networkFile))
	if err != nil {
		t.Fatalf("%s: %v", n.Name, err)
	}
	return network
}

// infoOf is `orama global tor info --json` on n: one entry per installed role.
func infoOf(t *testing.T, f *fleet.Fleet, n fleet.Node) []tornet.NodeInfo {
	t.Helper()
	out := infra.OnNode(t, f, n, "global", "tor", "info", "--json")
	infra.ExpectNodeExit(t, n.Name+" tor info", out, infra.ExitOK)
	var infos []tornet.NodeInfo
	if err := json.Unmarshal([]byte(out.Stdout), &infos); err != nil {
		t.Fatalf("%s: tor info --json: %v\n%s", n.Name, err, out.Stdout)
	}
	return infos
}

// homeInfo is the entry of the role whose DataDirectory is home.
func homeInfo(t *testing.T, infos []tornet.NodeInfo, n fleet.Node, home string) tornet.NodeInfo {
	t.Helper()
	for _, i := range infos {
		if i.Home == home {
			return i
		}
	}
	t.Fatalf("%s: tor info has no entry for %s: %+v", n.Name, home, infos)
	return tornet.NodeInfo{}
}

// cleanup runs cmd on n after the test, with a context of its own: the test's
// is cancelled by then.
func cleanup(t *testing.T, f *fleet.Fleet, n fleet.Node, cmd string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), fleet.CleanupBudget)
	defer cancel()
	out, err := f.SSH(ctx, n).Run(ctx, cmd)
	if err != nil || out.Exit != 0 {
		t.Errorf("cleanup on %s (%s) failed: %v exit %d %s — the node may be left disturbed", n.Name, cmd, err, out.Exit, out.Stderr)
	}
}

func randomSuffix(t *testing.T) string {
	t.Helper()
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

// circuitClient is a throwaway client of the Orama network on a node: a tor
// process of its own (a transient unit, stopped when the test ends) that
// builds circuits through the network's authorities and relays, with a SOCKS
// listener on loopback.
type circuitClient struct {
	f     *fleet.Fleet
	n     fleet.Node
	socks int
}

// startClient starts a client of network on n. extra are torrc lines added
// after the client's own (to pin an exit, for example), and it returns when
// tor has bootstrapped.
func startClient(t *testing.T, f *fleet.Fleet, n fleet.Node, network tornet.Network, extra ...string) *circuitClient {
	t.Helper()
	unit := "e2e-tornet-" + randomSuffix(t)
	port := int(clientPortBase + clientPorts.Add(1))
	torrc, err := tornet.ClientTorrc(tornet.ClientConfig{Network: network, Home: "/run/" + unit, SOCKSPort: port})
	if err != nil {
		t.Fatal(err)
	}
	torrcPath := "/run/" + unit + ".torrc"
	f.WriteFile(t, n, torrcPath, []byte(torrc+strings.Join(extra, "\n")+"\n"), 0o644)
	f.MustExec(t, n, fmt.Sprintf("systemd-run --quiet --unit %s -p User=debian-tor -p RuntimeDirectory=%s -p RuntimeDirectoryMode=0700 -p NoNewPrivileges=yes /usr/bin/tor -f %s", unit, unit, torrcPath))
	t.Cleanup(func() {
		cleanup(t, f, n, "systemctl stop "+unit+" && systemctl reset-failed "+unit+" 2>/dev/null; true")
	})
	eventually.Require(t, pollEvery, bootstrapBudget, "the client of the Orama network on "+n.Name+" to bootstrap", func() (bool, error) {
		out := f.Exec(t, n, "journalctl -u "+unit+" --no-pager | grep -c 'Bootstrapped 100%'")
		return strings.TrimSpace(out.Stdout) != "0" && out.Exit == 0, nil
	})
	return &circuitClient{f: f, n: n, socks: port}
}

// fetch GETs url through the client's circuits and returns the HTTP status (0
// when no response arrived) and the body.
func (c *circuitClient) fetch(t *testing.T, url string, curlArgs ...string) (int, string) {
	t.Helper()
	const mark = "\n__STATUS__"
	args := strings.Join(curlArgs, " ")
	out := c.f.Exec(t, c.n, fmt.Sprintf("curl -sS --max-time %d --socks5-hostname 127.0.0.1:%d %s -w %s %s", fetchSeconds, c.socks, args, fleet.ShellQuote(mark+"%{http_code}"), fleet.ShellQuote(url)))
	body, status, ok := strings.Cut(out.Stdout, mark)
	if !ok {
		return 0, out.Stdout + out.Stderr
	}
	var code int
	fmt.Sscanf(strings.TrimSpace(status), "%d", &code)
	return code, body
}
