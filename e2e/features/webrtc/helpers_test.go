//go:build e2e_fleet

package webrtc

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/services"
	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
	"github.com/DeBrosOfficial/network/e2e/harness/wallet"
)

// Topology and limits (website/src/docs/operator/webrtc-operations.mdx).
const (
	pathCreds       = "/v1/webrtc/turn/credentials"
	pathRooms       = "/v1/webrtc/rooms"
	turnUnit        = "orama-turn.service"
	turnYAML        = "/opt/orama/.orama/data/turn/turn.yaml"
	turnPort        = 3478
	turnsPort       = 5349
	relayLow        = 49152
	relayHigh       = 65535
	sfuSignalLow    = 30000
	sfuSignalHigh   = 30099
	turnNodes       = 2
	restTTL         = 24 * time.Hour
	sfuTTL          = 600 * time.Second
	refreshFraction = 0.8
	secretBytes     = 32
	cleanupBudget   = 5 * time.Minute
	readyBudget     = 5 * time.Minute
	mediaBudget     = 2 * time.Minute
	pollEvery       = 3 * time.Second
	dialBudget      = 15 * time.Second
)

// cliKeyShape is the key `orama namespace keys create` prints once, on a line
// of its own; the id is on an "id:" line.
var (
	cliKeyShape = regexp.MustCompile(`(?m)^\s*(orama_(?:sk|rk)_[0-9A-Za-z]+_[0-9A-Za-z]+)\s*$`)
	cliKeyID    = regexp.MustCompile(`(?m)^\s+id:\s+(\d+)\s*$`)
)

// runtimeKey mints an app-runtime key with the operator's CLI (the namespace
// has no HTTP owner session: ViaOperator) and revokes it at cleanup.
func runtimeKey(t *testing.T, n *ns.Namespace) string {
	t.Helper()
	out := n.CLI.MustOK(t, "namespace", "keys", "create", "--scope", "app-runtime", "--label", "e2e-webrtc").Stdout
	key, id := cliKeyShape.FindStringSubmatch(out), cliKeyID.FindStringSubmatch(out)
	if key == nil || id == nil {
		t.Fatalf("keys create printed no key or id: %q", out)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), cleanupBudget)
		defer cancel()
		if res, err := n.CLI.Run(ctx, "namespace", "keys", "revoke", "--id", id[1]); err != nil || res.Exit != 0 {
			t.Errorf("cleanup: revoking key %s in %s: %v %s", id[1], n.Name, err, res.Stderr)
		}
	})
	return key[1]
}

func sfuUnit(name string) string { return "orama-namespace-sfu@" + name + ".service" }

// fixture is a namespace with WebRTC enabled through the operator's CLI and
// a runtime member (who holds the webrtc grant) signed in.
type fixture struct {
	f     *fleet.Fleet
	n     *ns.Namespace
	c     *gw.Client
	token string
	// members are the nodes the namespace is placed on; a larger fleet has
	// others, which run none of its services.
	members []fleet.Node
}

// setup creates the namespace and enables WebRTC; the cleanup disables it.
func setup(t *testing.T) *fixture {
	t.Helper()
	f := harness.Fleet(t)
	if len(f.State.Nodes) < 3 {
		harness.SkipNotApplicable(t, "WebRTC needs a three-member namespace; the run has fewer than three nodes")
	}
	tenancy.Reserve(t, harness.Fleet(t), 1)
	n := ns.New(t, f, ns.Options{Via: ns.ViaOperator})
	n.CLI.MustOK(t, "namespace", "enable", "webrtc", "--namespace", n.Name)
	t.Cleanup(func() { disable(t, n) })
	c := harness.GW(t).WithBase(gw.NamespaceURL(f.State, n.Name))
	return &fixture{f: f, n: n, c: c, token: member(t, n, "runtime"), members: tenancy.Members(t, f, n.Name)}
}

func disable(t *testing.T, n *ns.Namespace) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), cleanupBudget)
	defer cancel()
	if res, err := n.CLI.Run(ctx, "namespace", "disable", "webrtc", "--namespace", n.Name); err != nil || res.Exit != 0 {
		t.Errorf("cleanup: disabling WebRTC on %s: %v %s", n.Name, err, res.Stderr)
	}
}

// member adds a fresh wallet with role and returns its session token.
// The member signs in at the index gateway, the one that signs everybody in
// (the namespace's own gateway is the data plane).
func member(t *testing.T, n *ns.Namespace, role string) string {
	t.Helper()
	w, err := wallet.NewEVM()
	if err != nil {
		t.Fatal(err)
	}
	n.CLI.MustOK(t, "members", "add", w.Address(), "--role", role)
	s, err := harness.GW(t).For(t).SignIn(t.Context(), w, n.Name, nil)
	if err != nil {
		t.Fatal(err)
	}
	return s.AccessToken
}

// restCreds asks the REST route for TURN credentials as token.
func restCreds(t testing.TB, c *gw.Client, token string) (*gw.Response, services.TURNCreds) {
	t.Helper()
	var cr services.TURNCreds
	r := c.MustSend(t, gw.Req{Method: http.MethodPost, Path: pathCreds, Bearer: token})
	if r.Status == http.StatusOK {
		if err := r.Decode(&cr); err != nil {
			t.Fatal(err)
		}
	}
	return r, cr
}

// turnHolders are the nodes whose shared TURN lists the namespace as a tenant.
func turnHolders(t testing.TB, f *fleet.Fleet, name string) []fleet.Node {
	t.Helper()
	var holders []fleet.Node
	for _, node := range f.State.Nodes {
		out := f.Exec(t, node, "grep -cE -- "+fleet.ShellQuote(`namespace: "?`+name+`"?$`)+" "+turnYAML)
		if out.Exit == 0 && strings.TrimSpace(out.Stdout) != "0" {
			holders = append(holders, node)
		}
	}
	return holders
}
