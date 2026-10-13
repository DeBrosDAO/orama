//go:build e2e_fleet

package serverless

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
	"github.com/DeBrosOfficial/network/e2e/harness/wallet"
)

const pathWSConnections = "/v1/serverless/ws/connections"

// TestContext_deviceID: get_caller_device_id is the session's device
// thumbprint for a device-bound session and empty otherwise
// (website/src/docs/developer/functions.mdx#context; docs/whitepaper/technical-reference/vol1/13-identity.md#devices).
func TestContext_deviceID(t *testing.T) {
	t.Parallel()
	fx := setup(t)
	deploy(t, fx, fnSpec{name: "e2e-dev"})
	w, err := wallet.NewEVM()
	if err != nil {
		t.Fatal(err)
	}
	fx.n.CLI.MustOK(t, "members", "add", w.Address(), "--role", roleRuntime)
	dev := gw.NewDevice(t, wallet.AlgEd25519)
	bound, err := fx.c.For(t).SignIn(t.Context(), w, fx.n.Name, dev)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := invoke(t, fx.c, "e2e-dev", bound.AccessToken, map[string]any{"op": "whoami"}).Expect(t, http.StatusOK).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out["device"] != dev.ID() {
		t.Errorf("device-bound caller: get_caller_device_id %v, want %s", out["device"], dev.ID())
	}
	if got := call(t, fx, "e2e-dev", map[string]any{"op": "whoami"})["device"]; got != "" {
		t.Errorf("a session with no device reports device %v", got)
	}
}

// TestWSConnections_ownedRead: the socket registry answers the namespace's
// fn:read holders and refuses anonymous callers (docs/whitepaper/technical-reference/appendices/i-api-surface.md#functions).
func TestWSConnections_ownedRead(t *testing.T) {
	t.Parallel()
	fx := setup(t)
	deploy(t, fx, fnSpec{name: "e2e-wsreg"})
	c := fx.c.PinTo(fx.f.State.Nodes[2].PublicIP)
	conn, _, err := c.DialWS(t.Context(), "/v1/functions/e2e-wsreg/ws", fx.admin, nil)
	if err != nil {
		t.Fatalf("opening the function socket: %v", err)
	}
	defer conn.Close()
	r := tenancy.Get(t, c, pathWSConnections, tenancy.Cred{Bearer: fx.admin})
	var body any
	if err := r.Expect(t, http.StatusOK).Decode(&body); err != nil {
		t.Fatalf("the registry is not JSON: %v", err)
	}
	tenancy.ExpectRefused(t, tenancy.Get(t, c, pathWSConnections, tenancy.Cred{}), http.StatusUnauthorized, tenancy.CodeMissing)
	if r := tenancy.Get(t, c, pathWSConnections+"/no-such-connection", tenancy.Cred{Bearer: fx.admin}); r.Status == http.StatusOK {
		t.Errorf("an unknown connection id answered 200: %.200s", r.Body)
	}
}

// TestDeploy_serverRefusesOutOfRangeLimits: the gateway itself must refuse (or
// clamp) memory and timeout outside 1-256 MB and 1-300 s — the CLI's check
// is client-side (core deploy_handler.go reads them with no range check).
func TestDeploy_serverRefusesOutOfRangeLimits(t *testing.T) {
	t.Parallel()
	fx := setup(t)
	dir := writeFixture(t, fnSpec{name: "e2e-huge"})
	fx.n.CLI.MustOK(t, "function", "build", dir)
	wasm := readFile(t, dir+"/function.wasm")
	body, ct := deployForm(t, "e2e-huge", wasm, map[string]string{"memory_limit_mb": "4096", "timeout_seconds": "100000"})
	r := fx.c.MustSend(t, gw.Req{Method: http.MethodPost, Path: "/v1/functions", Bearer: fx.admin, Header: http.Header{"Content-Type": {ct}}, Body: body})
	if r.Status >= 200 && r.Status < 300 {
		t.Cleanup(func() { deleteFn(t, fx.n.CLI, "e2e-huge") })
		info := fx.n.CLI.MustOK(t, "function", "get", "e2e-huge").Stdout
		if strings.Contains(info, "4096") || strings.Contains(info, "100000") {
			t.Errorf("the gateway stored memory 4096 MB / timeout 100000 s:\n%s", info)
		}
	}
	other := ns.UniqueName(t.Name())
	body, ct = deployForm(t, "e2e-foreign", wasm, map[string]string{"namespace": other})
	r = fx.c.MustSend(t, gw.Req{Method: http.MethodPost, Path: "/v1/functions", Bearer: fx.admin, Header: http.Header{"Content-Type": {ct}}, Body: body})
	if r.Status != http.StatusForbidden {
		t.Errorf("deploying into another namespace by form field: want 403, got %d", r.Status)
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func deployForm(t *testing.T, name string, wasm []byte, fields map[string]string) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fields["name"] = name
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	part, err := w.CreateFormFile("wasm", "function.wasm")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(wasm); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes(), w.FormDataContentType()
}

// TestDirectInvoke_path: POST /v1/invoke/<namespace>/<name>[@N] is the SDK's
// endpoint; a public function answers anonymously on the main gateway, a
// malformed path is 400 and a wrong method 405 (website/src/docs/developer/functions.mdx#http-api-reference).
func TestDirectInvoke_path(t *testing.T) {
	t.Parallel()
	fx := setup(t)
	deploy(t, fx, fnSpec{name: "e2e-direct", public: true})
	main := harness.GW(t)
	send := func(method, path string) *gw.Response {
		return main.MustSend(t, gw.Req{Method: method, Path: path, Header: http.Header{"Content-Type": {"application/json"}},
			Body: []byte(`{"op":"echo","value":"direct"}`)})
	}
	for _, p := range []string{"/v1/invoke/" + fx.n.Name + "/e2e-direct", "/v1/invoke/" + fx.n.Name + "/e2e-direct@1"} {
		if r := send(http.MethodPost, p); r.Status != http.StatusOK || !strings.Contains(string(r.Body), "direct") {
			t.Errorf("POST %s: %d %.200s", p, r.Status, r.Body)
		}
	}
	if r := send(http.MethodPost, "/v1/invoke/"+fx.n.Name); r.Status != http.StatusBadRequest {
		t.Errorf("a path without a function: want 400, got %d", r.Status)
	}
	if r := send(http.MethodGet, "/v1/invoke/"+fx.n.Name+"/e2e-direct"); r.Status != http.StatusMethodNotAllowed {
		t.Errorf("GET direct invoke: want 405, got %d", r.Status)
	}
	if r := send(http.MethodPost, "/v1/invoke/"+fx.n.Name+"/e2e-no-such-fn"); r.Status != http.StatusNotFound {
		t.Errorf("unknown function: want 404, got %d", r.Status)
	}
}

// TestDirectInvoke_throughTheNamespaceGatewayOneCORSAnswer: a page under the base domain calls a
// public function through its namespace gateway. The main gateway proxies to the namespace gateway,
// and both apply CORS; the answer must carry one Access-Control-Allow-Origin (a browser refuses two,
// which broke every such page until the proxy stopped adding the upstream's to its own), Vary: Origin
// and one Strict-Transport-Security (website/src/docs/developer/functions.mdx#http-api-reference).
func TestDirectInvoke_throughTheNamespaceGatewayOneCORSAnswer(t *testing.T) {
	t.Parallel()
	fx := setup(t)
	deploy(t, fx, fnSpec{name: "e2e-cors", public: true})
	origin := "https://app." + fx.f.State.BaseDomain
	r := fx.c.MustSend(t, gw.Req{Method: http.MethodPost, Path: "/v1/invoke/" + fx.n.Name + "/e2e-cors",
		Header: http.Header{"Content-Type": {"application/json"}, "Origin": {origin}},
		Body:   []byte(`{"op":"echo","value":"cors"}`)})
	if r.Status != http.StatusOK || !strings.Contains(string(r.Body), "cors") {
		t.Fatalf("POST through the namespace gateway: %d %.200s", r.Status, r.Body)
	}
	if got := r.Header.Values("Access-Control-Allow-Origin"); len(got) != 1 || got[0] != origin {
		t.Errorf("Access-Control-Allow-Origin = %q, want exactly [%q]", got, origin)
	}
	if !strings.Contains(strings.Join(r.Header.Values("Vary"), ","), "Origin") {
		t.Errorf("Vary = %q, want it to include Origin", r.Header.Values("Vary"))
	}
	if got := r.Header.Values("Strict-Transport-Security"); len(got) != 1 {
		t.Errorf("Strict-Transport-Security = %q, want one value", got)
	}
}
