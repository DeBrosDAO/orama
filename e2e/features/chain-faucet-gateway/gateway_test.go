//go:build e2e_fleet

package chainfaucetgateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

const (
	faucetPath = "/v1/chain/faucet"
	// funding is what the faucet account is given for fees: a drip costs it one transaction fee.
	funding = 20
	// drip is what the newcomer asks for, in ORAMA.
	drip = 5
	// perClientBurst is the faucet route's per-client burst (core/pkg/gateway/chain_tx_limit.go).
	perClientBurst = 3
	// tokenBudget bounds the wait for the per-client bucket to hold a token again (3 a minute).
	tokenBudget = 90 * time.Second
	pollEvery   = 5 * time.Second
	// routeBudget bounds the wait for the gateway to serve the faucet after the node restarts.
	routeBudget = 3 * time.Minute
)

var hashRE = regexp.MustCompile(`^[0-9A-F]{64}$`)

// initReport is `orama maint faucet init --json`.
type initReport struct {
	Address string `json:"address"`
	KeyFile string `json:"key_file"`
	Created bool   `json:"created"`
}

// answer is a faucet route's JSON answer, a drip or a refusal.
type answer struct {
	TxHash  string `json:"tx_hash"`
	Amount  string `json:"amount"`
	Height  string `json:"height"`
	Error   string `json:"error"`
	Message string `json:"message"`
}

// TestFaucetGateway_aNewcomerIsFundedThroughTheGatewayAlone walks the provisioning an operator does
// and then what a newcomer sees: the path is a 404 until the faucet is switched on, the key is made
// on the node and its account funded, and with the faucet on the gateway pays a fresh account, then
// explains each refusal. It restores node.yaml and removes the key when it ends.
func TestFaucetGateway_aNewcomerIsFundedThroughTheGatewayAlone(t *testing.T) {
	f := harness.Fleet(t)
	if f.State.IsStagenet() {
		harness.SkipNotApplicable(t, "the test writes node.yaml and restarts a node of the run's own fleet; it does not touch stagenet")
	}
	c := chain.New(t)
	n := c.FaucetNode(t)
	g := harness.GW(t).PinTo(n.PublicIP)
	params := readParams(t, c, n)

	if res := post(t, g, "application/json", `{"recipient":"`+c.NewKey(t, n, "e2e-faucet-gw-unserved").Address+`"}`); res.Status != http.StatusNotFound {
		t.Fatalf("POST %s on a gateway with no faucet key: HTTP %d, want 404: %s", faucetPath, res.Status, res.Body)
	}

	init := initKey(t, f, n)
	fundFaucetAccount(t, c, n, init.Address)
	enableFaucet(t, f, n)

	fresh := c.NewKey(t, n, "e2e-faucet-gw-fresh")
	amount := chain.Orama(drip)
	var a answer
	waitForRoute(t, g, `{"recipient":"`+fresh.Address+`","amount":"`+amount.String()+`"}`, &a)
	if !hashRE.MatchString(a.TxHash) || a.Amount != amount.String() || a.Height == "" || a.Height == "0" {
		t.Fatalf("the drip's answer is %+v: want the hash, the amount as a string and the block", a)
	}
	if got := c.Bank(t, n, fresh.Address); got.Cmp(amount) != 0 {
		t.Errorf("the recipient holds %s norama, want the %s it asked for", got.String(), amount.String())
	}

	// A second drip to the same recipient is the chain's cooldown, typed.
	expectRefusal(t, g, `{"recipient":"`+fresh.Address+`","amount":"`+amount.String()+`"}`, http.StatusTooManyRequests, "cooldown", params.cooldownText())

	// A module account cannot receive a drip, and the faucet is told so by the chain.
	expectRefusal(t, g, `{"recipient":"`+chain.ModuleAddress("fee_collector")+`"}`, http.StatusBadRequest, "bad_recipient", "")

	// One norama over the maximum is the chain's refusal of the amount.
	over := params.Params.MaxDrip.Add(chain.NewInt(1))
	expectRefusal(t, g, `{"recipient":"`+c.NewKey(t, n, "e2e-faucet-gw-over").Address+`","amount":"`+over.String()+`"}`, http.StatusBadRequest, "bad_amount", "")

	// The faucet account paid fees and nothing else: a drip is minted, not sent from it.
	if held := c.Bank(t, n, init.Address); held.Cmp(chain.Orama(funding)) > 0 {
		t.Errorf("the faucet account holds %s norama, more than the %d ORAMA it was given", held.String(), funding)
	}
	c.RequireInvariants(t, "gateway faucet drips")

	requireRateLimited(t, g)
}

// post sends one POST to the faucet route.
func post(t *testing.T, g *gw.Client, contentType, body string) *gw.Response {
	t.Helper()
	h := http.Header{}
	if contentType != "" {
		h.Set("Content-Type", contentType)
	}
	return g.MustSend(t, gw.Req{Method: http.MethodPost, Path: faucetPath, Header: h, Body: []byte(body)})
}

// limited reports whether res is the rate limiter's answer, which has no {error} of the faucet's.
func limited(res *gw.Response) bool {
	if res.Status != http.StatusTooManyRequests {
		return false
	}
	var a answer
	return json.Unmarshal(res.Body, &a) != nil || a.Error == ""
}

// postPaced sends one JSON request and, if the per-client bucket is empty, waits for a token
// (polling, not sleeping) and sends it again.
func postPaced(t *testing.T, g *gw.Client, body string) *gw.Response {
	t.Helper()
	var res *gw.Response
	eventually.Require(t, pollEvery, tokenBudget, "a token in the faucet route's per-client bucket", func() (bool, error) {
		res = post(t, g, "application/json", body)
		if limited(res) {
			return false, fmt.Errorf("rate limited")
		}
		return true, nil
	})
	return res
}

// waitForRoute sends the first drip once the gateway serves the route after the node's restart.
func waitForRoute(t *testing.T, g *gw.Client, body string, into *answer) {
	t.Helper()
	eventually.Require(t, pollEvery, routeBudget, "the gateway to serve the faucet", func() (bool, error) {
		res := post(t, g, "application/json", body)
		if res.Status == http.StatusNotFound || res.Status >= http.StatusBadGateway {
			return false, fmt.Errorf("HTTP %d: %s", res.Status, res.Body)
		}
		if res.Status != http.StatusOK {
			t.Fatalf("the first drip was refused: HTTP %d: %s", res.Status, res.Body)
		}
		return true, json.Unmarshal(res.Body, into)
	})
}

// expectRefusal sends body and expects the faucet's typed refusal; reason, when given, must be in the message.
func expectRefusal(t *testing.T, g *gw.Client, body string, status int, kind, reason string) {
	t.Helper()
	res := postPaced(t, g, body)
	var a answer
	if err := json.Unmarshal(res.Body, &a); err != nil {
		t.Fatalf("the refusal is not JSON: HTTP %d %s", res.Status, res.Body)
	}
	if res.Status != status || a.Error != kind || a.Message == "" || strings.ContainsAny(a.Message, "\n\x1b") {
		t.Errorf("HTTP %d %+v, want %d %s with a one-line reason", res.Status, a, status, kind)
	}
	if reason != "" && !strings.Contains(a.Message, reason) {
		t.Errorf("the reason %q lacks the chain's words %q", a.Message, reason)
	}
}

// requireRateLimited empties the per-client bucket and meets the limiter's 429 with a Retry-After.
func requireRateLimited(t *testing.T, g *gw.Client) {
	t.Helper()
	for i := 0; i < perClientBurst*3; i++ {
		res := post(t, g, "text/plain", "x")
		if limited(res) {
			if res.Header.Get("Retry-After") == "" {
				t.Error("the faucet route's 429 has no Retry-After")
			}
			return
		}
		if res.Status != http.StatusUnsupportedMediaType {
			t.Fatalf("a request that is not JSON: HTTP %d, want 415: %s", res.Status, res.Body)
		}
	}
	t.Errorf("%d requests in a row were not rate limited: the route's per-client burst is %d", perClientBurst*3, perClientBurst)
}

type emissionParams struct {
	Params struct {
		Enabled  bool      `json:"faucet_enabled"`
		MaxDrip  chain.Int `json:"faucet_max_drip"`
		Cooldown chain.Int `json:"faucet_recipient_cooldown_seconds"`
	} `json:"params"`
}

func (emissionParams) cooldownText() string { return "still within its cooldown" }

func readParams(t *testing.T, c *chain.Chain, n fleet.Node) emissionParams {
	t.Helper()
	var p emissionParams
	c.Query(t, n, &p, "emission", "params")
	if !p.Params.Enabled || p.Params.MaxDrip.IsZero() || p.Params.Cooldown.IsZero() {
		t.Fatalf("the run chain's genesis does not give a usable faucet: %+v", p.Params)
	}
	return p
}

// initKey runs `orama maint faucet init` on the node, twice: the second run finds the key.
func initKey(t *testing.T, f *fleet.Fleet, n fleet.Node) initReport {
	t.Helper()
	run := func() initReport {
		out := infra.OnNode(t, f, n, "maint", "faucet", "init", "--json")
		infra.ExpectNodeExit(t, "orama maint faucet init on "+n.Name, out, infra.ExitOK)
		var rep initReport
		if err := json.Unmarshal([]byte(out.Stdout), &rep); err != nil {
			t.Fatalf("init printed %q: %v", out.Stdout, err)
		}
		return rep
	}
	first := run()
	t.Cleanup(func() { f.Exec(t, n, "rm -f "+fleet.ShellQuote(first.KeyFile)) })
	if !first.Created || !strings.HasPrefix(first.Address, "orama1") {
		t.Fatalf("init = %+v, want a new key and its account", first)
	}
	if again := run(); again.Created || again.Address != first.Address {
		t.Fatalf("a second init = %+v, want the same account and no new key", again)
	}
	// The key is the gateway's alone: its owner may read it, nobody else.
	stat := f.Exec(t, n, "stat -c '%U %a' "+fleet.ShellQuote(first.KeyFile))
	if got := strings.TrimSpace(stat.Stdout); stat.Exit != 0 || got != "orama 600" {
		t.Errorf("the key file is %q (exit %d), want owned by orama with mode 600", got, stat.Exit)
	}
	return first
}

// fundFaucetAccount gives the faucet account its fees by a drip signed on the operator's node: a
// genesis account is refused by the chain (a genesis starts at zero supply).
func fundFaucetAccount(t *testing.T, c *chain.Chain, n fleet.Node, address string) {
	t.Helper()
	if got := c.Fund(t, n, address, chain.Orama(funding)); got.Cmp(chain.Orama(funding)) != 0 {
		t.Fatalf("the faucet account holds %s norama after its funding, want %d ORAMA", got.String(), funding)
	}
}

// enableFaucet switches chain.faucet on in the node's node.yaml, restarts the node, and restores
// both when the test ends.
func enableFaucet(t *testing.T, f *fleet.Fleet, n fleet.Node) {
	t.Helper()
	backup := infra.NodeConfigPath + ".e2e-faucet"
	for _, cmd := range []string{
		fmt.Sprintf("cp -p %s %s", infra.NodeConfigPath, backup),
		fmt.Sprintf("printf '\\nchain:\\n  faucet:\\n    enabled: true\\n' >> %s", infra.NodeConfigPath),
	} {
		if out := f.Exec(t, n, "sudo sh -c "+fleet.ShellQuote(cmd)); out.Exit != 0 {
			t.Fatalf("%s: %q failed: %s", n.Name, cmd, f.Redact(out.Stdout+out.Stderr))
		}
	}
	t.Cleanup(func() {
		restore := fmt.Sprintf("mv -f %s %s", backup, infra.NodeConfigPath)
		if out := f.Exec(t, n, "sudo sh -c "+fleet.ShellQuote(restore)); out.Exit != 0 {
			t.Errorf("%s: could not restore node.yaml: %s", n.Name, f.Redact(out.Stdout+out.Stderr))
			return
		}
		restart(t, f, n)
	})
	restart(t, f, n)
}

func restart(t *testing.T, f *fleet.Fleet, n fleet.Node) {
	t.Helper()
	out := infra.OnNode(t, f, n, "node", "restart")
	if out.Exit != infra.ExitOK {
		t.Fatalf("orama node restart on %s: exit %d\n%s", n.Name, out.Exit, f.Redact(out.Stdout+out.Stderr))
	}
	infra.WaitConverged(t, len(f.State.Nodes), infra.ConvergeBudget, n.Name+" after orama node restart")
}
