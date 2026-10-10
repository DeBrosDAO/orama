//go:build e2e_fleet

package serverless

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

// mintKey mints a key with scope as the admin member and revokes it at cleanup.
func mintKey(t *testing.T, fx *fixture, scope string) string {
	t.Helper()
	var k struct {
		ID     int64  `json:"id"`
		APIKey string `json:"api_key"`
	}
	r := tenancy.Post(t, fx.c, tenancy.PathKeys, tenancy.Cred{Bearer: fx.admin}, map[string]any{"scope": scope, "label": "e2e-" + scope})
	if err := r.Expect(t, http.StatusCreated).Decode(&k); err != nil || k.APIKey == "" {
		t.Fatalf("minting a %s key: %v", scope, err)
	}
	if err := fx.c.Protect(k.APIKey); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), cleanupLimit)
		defer cancel()
		if _, err := fx.c.Send(ctx, gw.Req{Method: http.MethodDelete, Path: tenancy.PathKeys + "/" + strconv.FormatInt(k.ID, 10), Bearer: fx.admin}); err != nil {
			t.Errorf("cleanup: revoking key %d: %v", k.ID, err)
		}
	})
	return k.APIKey
}

// invokeKey invokes fn with an API key instead of a bearer.
func invokeKey(t *testing.T, fx *fixture, fn, key string) *gw.Response {
	t.Helper()
	return fx.c.MustSend(t, gw.Req{Method: http.MethodPost, Path: "/v1/functions/" + fn + "/invoke", APIKey: key,
		Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"op":"echo","value":"k"}`)})
}

// TestInvoke_accessMatrix: a private function needs a signed-in wallet or
// the invoke grant (a storage-only key is refused); a public one is open;
// `name@N` runs that version (website/src/docs/developer/functions.mdx#functionyaml, #versioning).
func TestInvoke_accessMatrix(t *testing.T) {
	t.Parallel()
	fx := setup(t)
	deploy(t, fx, fnSpec{name: "e2e-private", yaml: "env:\n  MARK: v1\n"})
	deploy(t, fx, fnSpec{name: "e2e-public", public: true})
	if r := invoke(t, fx.c, "e2e-private", "", map[string]any{"op": "echo"}); r.Status != http.StatusUnauthorized && r.Status != http.StatusForbidden {
		t.Errorf("anonymous invoke of a private function: %d", r.Status)
	}
	if r := invoke(t, fx.c, "e2e-public", "", map[string]any{"op": "echo", "value": "anon"}); r.Status != http.StatusOK {
		t.Errorf("anonymous invoke of a public function: %d %.200s", r.Status, r.Body)
	}
	if r := invoke(t, fx.c, "e2e-private", fx.runtime, map[string]any{"op": "echo"}); r.Status != http.StatusOK {
		t.Errorf("runtime member invoking a private function: %d", r.Status)
	}
	if r := invokeKey(t, fx, "e2e-private", mintKey(t, fx, "invoke-only")); r.Status != http.StatusOK {
		t.Errorf("invoke-only key: %d %.200s", r.Status, r.Body)
	}
	// A storage-only key lacks the invoke grant: an identified caller that is
	// refused gets 403 (serverless/invoke.go canInvokeFn, handler
	// classifyInvokeError); 401 is for a caller with no identity.
	if r := invokeKey(t, fx, "e2e-private", mintKey(t, fx, "storage")); r.Status != http.StatusForbidden {
		t.Errorf("a storage-only key invoking a private function: want 403, got %d %.200s", r.Status, r.Body)
	}
	for name, bearer := range map[string]string{"garbage": "x.y.z", "expired-looking": strings.Repeat("a", 40)} {
		if r := invoke(t, fx.c, "e2e-private", bearer, map[string]any{"op": "echo"}); r.Status != http.StatusUnauthorized {
			t.Errorf("%s bearer: want 401, got %d", name, r.Status)
		}
	}
	if r := invoke(t, fx.c, "e2e-no-such-fn", fx.admin, map[string]any{}); r.Status != http.StatusNotFound || rpcCode(r) != codeNotFound {
		t.Errorf("unknown function: want 404 NOT_FOUND, got %d %s", r.Status, rpcCode(r))
	}
}

// TestInvoke_versionPinned: after a second deploy, name@1 still runs the
// first version and the bare name runs the latest.
func TestInvoke_versionPinned(t *testing.T) {
	t.Parallel()
	fx := setup(t)
	dir := deploy(t, fx, fnSpec{name: "e2e-ver", yaml: "env:\n  MARK: v1\n"})
	redeploy(t, fx, dir, "env:\n  MARK: v2\n")
	for fn, want := range map[string]string{"e2e-ver@1": "v1", "e2e-ver@2": "v2", "e2e-ver": "v2"} {
		if got := call(t, fx, fn, map[string]any{"op": "whoami", "key": "MARK"})["env"]; got != want {
			t.Errorf("%s ran MARK=%v, want %s", fn, got, want)
		}
	}
	if r := invoke(t, fx.c, "e2e-ver@99", fx.admin, map[string]any{}); r.Status != http.StatusNotFound {
		t.Errorf("a version that does not exist: want 404, got %d", r.Status)
	}
}

// TestInvoke_callerContext: the function sees the caller's wallet and JWT
// subject, a per-invocation request id, and nothing for an anonymous call.
func TestInvoke_callerContext(t *testing.T) {
	t.Parallel()
	fx := setup(t)
	deploy(t, fx, fnSpec{name: "e2e-ctx", public: true})
	a := call(t, fx, "e2e-ctx", map[string]any{"op": "whoami"})
	b := call(t, fx, "e2e-ctx", map[string]any{"op": "whoami"})
	if a["wallet"] == "" || a["subject"] == "" || a["request_id"] == "" || a["request_id"] == b["request_id"] {
		t.Errorf("caller context %v / %v", a, b)
	}
	var anon map[string]any
	if err := invoke(t, fx.c, "e2e-ctx", "", map[string]any{"op": "whoami"}).Expect(t, http.StatusOK).Decode(&anon); err != nil {
		t.Fatal(err)
	}
	if anon["subject"] != "" || anon["device"] != "" {
		t.Errorf("an anonymous call reports a caller: %v", anon)
	}
}

// TestInvoke_nested: function_invoke runs another function of the namespace
// with the caller's identity; from an anonymous public call a private target
// is not reachable (website/src/docs/developer/functions.mdx#capabilities: a nested call with no
// caller reaches only public functions).
func TestInvoke_nested(t *testing.T) {
	t.Parallel()
	fx := setup(t)
	deploy(t, fx, fnSpec{name: "e2e-outer", public: true})
	deploy(t, fx, fnSpec{name: "e2e-inner"})
	nested := map[string]any{"op": "nested", "name": "e2e-inner", "data": map[string]any{"op": "echo", "value": "deep"}}
	if res := sub(call(t, fx, "e2e-outer", nested), "result"); res["echo"] != "deep" {
		t.Errorf("nested invoke as the admin returned %v", res)
	}
	var anon map[string]any
	if err := invoke(t, fx.c, "e2e-outer", "", nested).Expect(t, http.StatusOK).Decode(&anon); err != nil {
		t.Fatal(err)
	}
	if anon["result"] != nil {
		t.Errorf("an anonymous nested call reached a private function: %v", anon["result"])
	}
}

// TestInvoke_timeoutIsRateLimited: running past timeout on a direct invoke is
// 429 RATE_LIMITED, retryable (website/src/docs/developer/functions.mdx#functionyaml).
func TestInvoke_timeoutIsRateLimited(t *testing.T) {
	t.Parallel()
	fx := setup(t)
	deploy(t, fx, fnSpec{name: "e2e-slow", yaml: "timeout: 1\n"})
	r := invoke(t, fx.c, "e2e-slow", fx.admin, map[string]any{"op": "spin", "ms": 5000})
	if r.Status != http.StatusTooManyRequests || rpcCode(r) != codeRateLimited {
		t.Errorf("a 5s spin under timeout 1: want 429 %s, got %d %.300s", codeRateLimited, r.Status, r.Body)
	}
	call(t, fx, "e2e-slow", map[string]any{"op": "spin", "ms": 100})
}

// TestInvoke_memoryLimit: allocating past memory fails the invocation;
// within it succeeds (docs/whitepaper/technical-reference/vol1/21-serverless.md: WithMemoryLimitPages).
func TestInvoke_memoryLimit(t *testing.T) {
	t.Parallel()
	fx := setup(t)
	deploy(t, fx, fnSpec{name: "e2e-mem", yaml: "memory: 16\n"})
	call(t, fx, "e2e-mem", map[string]any{"op": "alloc", "size": 2})
	// The guest traps when its memory cannot grow; the invoker reports a
	// failed execution (classifyInvokeError's default).
	r := invoke(t, fx.c, "e2e-mem", fx.admin, map[string]any{"op": "alloc", "size": 64})
	if r.Status != http.StatusInternalServerError || rpcCode(r) != codeExecFailed {
		t.Errorf("a 64 MB allocation under memory 16: want 500 %s, got %d %.200s", codeExecFailed, r.Status, r.Body)
	}
}

// TestInvoke_unknownHostModule: importing from a module other than env, host
// or orama fails at instantiation with a readable error, not a crash.
func TestInvoke_unknownHostModule(t *testing.T) {
	t.Parallel()
	fx := setup(t)
	deploy(t, fx, fnSpec{name: "e2e-badimport", src: badImportDir})
	r := invoke(t, fx.c, "e2e-badimport", fx.admin, map[string]any{})
	if r.Status != http.StatusInternalServerError || rpcCode(r) != codeExecFailed || !strings.Contains(string(r.Body), "nosuchmodule") {
		t.Errorf("unknown host module: want 500 %s naming the module, got %d %.300s", codeExecFailed, r.Status, r.Body)
	}
}

// TestInvoke_concurrencyBounded: one namespace cannot run unbounded WASM at
// once on a gateway (docs/whitepaper/technical-reference/vol1/21-serverless.md: a per-namespace slot); a burst queues
// and still completes.
func TestInvoke_concurrencyBounded(t *testing.T) {
	t.Parallel()
	fx := setup(t)
	deploy(t, fx, fnSpec{name: "e2e-busy", yaml: "timeout: 60\n"})
	const burst, spin = 12, 4 * time.Second
	c := fx.c.PinTo(fx.f.State.Nodes[0].PublicIP)
	var wg sync.WaitGroup
	statuses := make([]int, burst)
	errs := make([]error, burst)
	start := time.Now()
	for i := 0; i < burst; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r, err := c.Send(t.Context(), gw.Req{Method: http.MethodPost, Path: "/v1/functions/e2e-busy/invoke", Bearer: fx.admin,
				Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"op":"spin","ms":4000}`)})
			if err != nil {
				errs[i] = err
				return
			}
			statuses[i] = r.Status
		}(i)
	}
	wg.Wait()
	for i, s := range statuses {
		if errs[i] != nil {
			t.Errorf("invocation %d got no answer after %s: %v", i, time.Since(start).Round(time.Second), errs[i])
			continue
		}
		if s != http.StatusOK && s != http.StatusTooManyRequests {
			t.Errorf("invocation %d: %d", i, s)
		}
	}
	if took := time.Since(start); took < 2*spin {
		t.Errorf("%d concurrent %s spins finished in %s: nothing bounded the namespace", burst, spin, took)
	}
}

// TestInvoke_crossNamespace: another namespace's credential cannot invoke a
// private function or manage functions here (NAMESPACE_MISMATCH / 403), and
// the main gateway refuses an anonymous invoke that names no namespace
// (website/src/docs/developer/functions.mdx#http-api-reference; docs/whitepaper/technical-reference/vol1/21-serverless.md, bugboard #423/#427).
func TestInvoke_crossNamespace(t *testing.T) {
	t.Parallel()
	fx := setupN(t, 2)
	deploy(t, fx, fnSpec{name: "e2e-home"})
	other := ns.New(t, fx.f, ns.Options{})
	foreign := other.Owner.Token()
	main := harness.GW(t)
	q := url.Values{"namespace": {fx.n.Name}}
	r := main.MustSend(t, gw.Req{Method: http.MethodPost, Path: "/v1/functions/e2e-home/invoke", Query: q, Bearer: foreign, Body: []byte(`{}`)})
	tenancy.ExpectDenied(t, r, "invoking another namespace's private function")
	r = main.MustSend(t, gw.Req{Path: "/v1/functions", Query: q, Bearer: foreign})
	if r.Status == http.StatusOK && strings.Contains(string(r.Body), "e2e-home") {
		t.Errorf("another namespace listed this namespace's functions")
	}
	r = main.MustSend(t, gw.Req{Method: http.MethodDelete, Path: "/v1/functions/e2e-home", Query: q, Bearer: foreign})
	tenancy.ExpectDenied(t, r, "deleting another namespace's function")
	r = main.MustSend(t, gw.Req{Method: http.MethodPost, Path: "/v1/functions/e2e-home/invoke", Body: []byte(`{}`)})
	if r.Status != http.StatusBadRequest {
		t.Errorf("anonymous invoke naming no namespace on the main gateway: want 400, got %d", r.Status)
	}
	call(t, fx, "e2e-home", map[string]any{"op": "echo"})
}
