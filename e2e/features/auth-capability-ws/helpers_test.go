//go:build e2e_fleet

package authcapabilityws

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/evidence"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
	"github.com/DeBrosOfficial/network/e2e/harness/wallet"
)

const (
	fixtureDir   = "testdata/capfn"
	capFunction  = "e2e-capws"
	plainFunc    = "e2e-plainws"
	dialBudget   = 15 * time.Second
	cleanupLimit = 2 * time.Minute
	fixturePerm  = 0o644
	// errBodyBytes bounds the refusal body read off a failed handshake.
	errBodyBytes = 4096
)

// fixture is a namespace with the capability function and a plain function
// deployed through the CLI, and an end user holding the runtime role.
type fixture struct {
	f     *fleet.Fleet
	n     *ns.Namespace
	c     *gw.Client
	user  *wallet.EVM
	dev   *wallet.Device
	bound *gw.Session // device-bound: may mint
	plain *gw.Session // bound to no device
}

func setup(t *testing.T) *fixture {
	t.Helper()
	if _, err := exec.LookPath("tinygo"); err != nil {
		harness.SkipNotApplicable(t, "tinygo is not on the runner's PATH; `orama function deploy` builds functions with it")
	}
	f := harness.Fleet(t)
	n := ns.New(t, f, ns.Options{Via: ns.ViaOperator})
	deploy(t, n, capFunction, "ws_auth: capability\n")
	deploy(t, n, plainFunc, "")
	fx := &fixture{f: f, n: n, c: harness.GW(t), user: newWallet(t), dev: gw.NewDevice(t, wallet.AlgEd25519)}
	n.CLI.MustOK(t, "members", "add", fx.user.Address(), "--role", "runtime")
	fx.bound = signIn(t, fx.c, fx.user, n.Name, fx.dev)
	fx.plain = signIn(t, fx.c, fx.user, n.Name, nil)
	return fx
}

// deploy copies the fixture to a scratch directory (deploy builds into it),
// writes its function.yaml and deploys it as the namespace operator.
func deploy(t *testing.T, n *ns.Namespace, name, extraYAML string) {
	t.Helper()
	dir := t.TempDir()
	for _, file := range []string{"function.go", "go.mod"} {
		src, err := os.ReadFile(filepath.Join(fixtureDir, file))
		if err != nil {
			t.Fatalf("fixture %s: %v", file, err)
		}
		if err := os.WriteFile(filepath.Join(dir, file), src, fixturePerm); err != nil {
			t.Fatal(err)
		}
	}
	yaml := "name: " + name + "\npublic: false\nmemory: 64\ntimeout: 30\n" + extraYAML
	if err := os.WriteFile(filepath.Join(dir, "function.yaml"), []byte(yaml), fixturePerm); err != nil {
		t.Fatal(err)
	}
	n.CLI.MustOK(t, "function", "deploy", dir)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), cleanupLimit)
		defer cancel()
		if res, err := n.CLI.Run(ctx, "function", "delete", name, "--force"); err != nil || res.Exit != 0 {
			t.Errorf("cleanup: failed to delete function %s: %v %s", name, err, res.Stderr)
		}
	})
}

func newWallet(t testing.TB) *wallet.EVM {
	t.Helper()
	w, err := wallet.NewEVM()
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func signIn(t testing.TB, c *gw.Client, w *wallet.EVM, namespace string, d *wallet.Device) *gw.Session {
	t.Helper()
	s, err := c.For(t).SignIn(t.Context(), w, namespace, d)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// invoke runs fn with body as bearer and returns the raw response.
func invoke(t testing.TB, fx *fixture, fn, bearer string, body any) *gw.Response {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return fx.c.MustSend(t, gw.Req{Method: http.MethodPost, Path: "/v1/functions/" + fn + "/invoke",
		Query: url.Values{"namespace": {fx.n.Name}}, Bearer: bearer,
		Header: http.Header{"Content-Type": {"application/json"}}, Body: raw})
}

// minted is what capability_mint returns (docs/SERVERLESS.md#capabilities).
type minted struct {
	Token        string `json:"token"`
	CapID        string `json:"cap_id"`
	Resource     string `json:"resource"`
	IssuerDevice string `json:"issuer_device"`
	ExpiresAt    any    `json:"expires_at"`
	Error        string `json:"error"`
}

// mint asks the function to mint a capability from session s.
func mint(t testing.TB, fx *fixture, s *gw.Session, resource string, ttl time.Duration) minted {
	t.Helper()
	var m minted
	resp := invoke(t, fx, capFunction, s.AccessToken, map[string]any{"op": "mint", "resource": resource, "ttl": int64(ttl.Seconds())})
	if err := resp.Expect(t, http.StatusOK).Decode(&m); err != nil {
		t.Fatal(err)
	}
	if m.Token != "" {
		protect(t, fx.c, m.Token)
	}
	return m
}

// capPath is the upgrade URL a sender presents: the capability and nothing else.
func capPath(fn, namespace, token string) string {
	return "/v1/functions/" + fn + "/ws?" + url.Values{"namespace": {namespace}, "cap": {token}}.Encode()
}

// dialAt opens a WebSocket on node n's public gateway (the capability rate
// limit and socket cap are per gateway), recording the handshake as evidence.
// On a refused upgrade it returns the status and body.
func dialAt(t testing.TB, c *gw.Client, n fleet.Node, pathQuery string, header http.Header) (*websocket.Conn, int, string) {
	t.Helper()
	d := websocket.Dialer{TLSClientConfig: c.TLS, HandshakeTimeout: gw.WSHandshakeBudget,
		NetDialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: dialBudget}).DialContext(ctx, network, net.JoinHostPort(n.PublicIP, "443"))
		}}
	u := "wss://" + strings.TrimPrefix(c.BaseURL, "https://") + pathQuery
	start := time.Now()
	conn, resp, err := d.DialContext(t.Context(), u, header)
	status, body := 0, ""
	if resp != nil {
		status = resp.StatusCode
		if resp.Body != nil {
			b, rerr := io.ReadAll(io.LimitReader(resp.Body, errBodyBytes))
			if rerr != nil && !errors.Is(rerr, io.EOF) {
				t.Errorf("reading the refused upgrade's body: %v", rerr)
			}
			body = string(b)
		}
	}
	rec := evidence.Record{Kind: evidence.KindHTTP, Test: t.Name(), Summary: "WS " + n.Name + " " + u,
		Status: status, DurationMS: time.Since(start).Milliseconds(), Output: body}
	if err != nil {
		rec.Error = err.Error()
	}
	if recErr := c.Recorder().Add(rec); recErr != nil {
		t.Errorf("failed to record the upgrade: %v", recErr)
	}
	if conn != nil {
		t.Cleanup(func() { conn.Close() })
	}
	return conn, status, strings.TrimSpace(body)
}

// closeCode reads until the server closes conn or within passes; 0 when the
// socket stayed open.
func closeCode(t testing.TB, conn *websocket.Conn, within time.Duration) int {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(within)); err != nil {
		t.Fatal(err)
	}
	for {
		_, _, err := conn.ReadMessage()
		if err == nil {
			continue
		}
		var ce *websocket.CloseError
		if errors.As(err, &ce) {
			return ce.Code
		}
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			return 0
		}
		t.Fatalf("socket failed without a close frame: %v", err)
	}
}

// protect registers a capability token with the evidence redactor: it is a
// bearer capability and must not reach the evidence file.
func protect(t testing.TB, c *gw.Client, values ...string) {
	t.Helper()
	red := c.Recorder().Redactor()
	if red == nil {
		return
	}
	if err := red.Add(values...); err != nil {
		t.Errorf("failed to register a capability for redaction: %v", err)
	}
}
