//go:build e2e_fleet

package authclusteradmin

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/edge"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
	"github.com/DeBrosOfficial/network/e2e/harness/wallet"
)

const (
	// pathNamespaces creates a namespace.
	pathNamespaces = "/v1/namespaces"
	// Namespace-creation modes (docs/whitepaper/technical-reference/appendices/d-cli-reference.md "orama maint cluster settings set").
	modeOperators = "operators"
	modeAllowlist = "allowlist"
	modeOpen      = "open"
	// settingMode and settingCap are the `orama maint cluster settings set` names.
	settingMode = "namespace-creation"
	settingCap  = "max-namespaces-per-wallet"
	// pollEvery paces readiness waits.
	pollEvery = time.Second
)

// settingLine reads "namespace-creation: open" / "max-namespaces-per-wallet: 10".
var settingLine = regexp.MustCompile(`(?m)^(namespace-creation|max-namespaces-per-wallet):\s*(\S+)\s*$`)

// clusterSettings is what `orama maint cluster settings show` prints.
type clusterSettings struct {
	Mode string
	Cap  int
}

func showSettings(t testing.TB, cli *oramacli.Runner) clusterSettings {
	t.Helper()
	out := cli.MustOK(t, "maint", "cluster", "settings", "show").Stdout
	var s clusterSettings
	for _, m := range settingLine.FindAllStringSubmatch(out, -1) {
		switch m[1] {
		case settingMode:
			s.Mode = m[2]
		case settingCap:
			n, err := strconv.Atoi(m[2])
			if err != nil {
				t.Fatalf("per-wallet cap %q is not a number", m[2])
			}
			s.Cap = n
		}
	}
	if s.Mode == "" || s.Cap == 0 {
		t.Fatalf("`orama maint cluster settings show` printed no mode or cap:\n%s", out)
	}
	return s
}

// setSetting changes one cluster setting and restores the value found, then
// verifies the restore: other stages expect the cluster exactly as stage 1
// left it.
func setSetting(t testing.TB, cli *oramacli.Runner, name, value string) {
	t.Helper()
	before := showSettings(t, cli)
	prev := before.Mode
	if name == settingCap {
		prev = strconv.Itoa(before.Cap)
	}
	cli.MustOK(t, "maint", "cluster", "settings", "set", name, value)
	t.Cleanup(func() {
		ctx, cancel := fleet.CleanupContext(t)
		defer cancel()
		res, err := cli.Run(ctx, "maint", "cluster", "settings", "set", name, prev)
		if err != nil || res.Exit != 0 {
			t.Errorf("cleanup: failed to restore %s to %s (exit %d): %v %s", name, prev, res.Exit, err, res.Stderr)
			return
		}
		out, err := cli.Run(ctx, "maint", "cluster", "settings", "show")
		if err != nil || !strings.Contains(out.Stdout, name+": "+prev) {
			t.Errorf("cleanup: %s was not restored to %s: %v\n%s", name, prev, err, out.Stdout)
		}
	})
}

// expectCode fails unless resp has status and code; a 401/403 must also carry
// a non-empty error and hint (docs/whitepaper/technical-reference/vol1/14-authorization.md#refusals-and-the-error-code-table).
func expectCode(t testing.TB, resp *gw.Response, status int, code string) {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(resp.Body, &body); err != nil || resp.Status != status || body["code"] != code {
		t.Fatalf("want HTTP %d %s, got %d: %.400s", status, code, resp.Status, resp.Body)
	}
	if s, _ := body["error"].(string); s == "" {
		t.Errorf("HTTP %d %s has no error text", status, code)
	}
}

func postJSON(t testing.TB, c *gw.Client, path, bearer string, v any) *gw.Response {
	t.Helper()
	body, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return c.MustSend(t, gw.Req{Method: http.MethodPost, Path: path, Bearer: bearer,
		Header: http.Header{"Content-Type": {"application/json"}}, Body: body})
}

func newWallet(t testing.TB) *wallet.EVM {
	t.Helper()
	w, err := wallet.NewEVM()
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// lobbyToken signs a wallet in to the lobby.
func lobbyToken(t testing.TB, c *gw.Client, w *wallet.EVM) string {
	t.Helper()
	s, err := c.For(t).SignIn(t.Context(), w, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	return s.AccessToken
}

// createAs asks to create name as bearer.
func createAs(t testing.TB, c *gw.Client, bearer, name string) *gw.Response {
	t.Helper()
	return postJSON(t, c, pathNamespaces, bearer, map[string]string{"name": name})
}

// nodeClient is a client whose every connection goes to one node.
type nodeClient struct {
	Node   fleet.Node
	Client *gw.Client
}

// perNode returns c pinned to each core node in turn (gw.Client.PinTo): same
// URL, same trust, same pacing as c.
func perNode(t testing.TB, f *fleet.Fleet, c *gw.Client) []nodeClient {
	t.Helper()
	out := make([]nodeClient, 0, len(f.State.Nodes))
	for _, n := range f.State.Nodes {
		out = append(out, nodeClient{Node: n, Client: c.PinTo(n.PublicIP)})
	}
	return out
}

// quiesce takes the run's credential burst before a flood and again, from a
// cleanup, after it: the flood meets a full product bucket on every gateway
// and leaves one behind for the run's paced clients (edge.Quiesce).
func quiesce(t *testing.T, f *fleet.Fleet) {
	t.Helper()
	if err := edge.Quiesce(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := fleet.CleanupContext(t)
		defer cancel()
		if err := edge.Quiesce(ctx); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	})
}
