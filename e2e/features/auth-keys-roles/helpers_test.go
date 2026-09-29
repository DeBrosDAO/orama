//go:build e2e_fleet

package authkeysroles

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
	"github.com/DeBrosOfficial/network/e2e/harness/wallet"
)

// Routes (docs/API_SURFACE.md) and roles (docs/AUTH.md#roles).
const (
	pathKeys    = "/v1/namespace/keys"
	pathMembers = "/v1/namespace/members"
	pathAudit   = "/v1/audit"
	pathGrants  = "/v1/deployments/grants"
	roleOwner   = "owner"
	roleAdmin   = "admin"
	roleDev     = "developer"
	roleRuntime = "runtime"
	roleReader  = "reader"
	// revocationStaleness and stalenessSlack bound a revocation's reach.
	revocationStaleness = 10 * time.Second
	stalenessSlack      = 5 * time.Second
	pollEvery           = time.Second
	cleanupBudget       = time.Minute
	// exit codes of clierr (cmd/orama/internal/clierr).
	exitUsage    = 2
	exitAuth     = 3
	exitNotFound = 4
	exitConflict = 6
)

// refusal fails unless resp is status with code and carries {error, code,
// hint} (docs/AUTH.md#when-a-request-is-refused); it returns the body.
func refusal(t testing.TB, resp *gw.Response, status int, code string) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(resp.Body, &body); err != nil || resp.Status != status || body["code"] != code {
		t.Fatalf("want HTTP %d %s, got %d: %.400s", status, code, resp.Status, resp.Body)
	}
	for _, k := range []string{"error", "hint"} {
		if s, _ := body[k].(string); strings.TrimSpace(s) == "" {
			t.Errorf("HTTP %d %s lacks %q", status, code, k)
		}
	}
	return body
}

func send(t testing.TB, c *gw.Client, method, path, bearer string, v any) *gw.Response {
	t.Helper()
	r := gw.Req{Method: method, Path: path, Bearer: bearer}
	if v != nil {
		body, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		r.Body, r.Header = body, http.Header{"Content-Type": {"application/json"}}
	}
	return c.MustSend(t, r)
}

func newWallet(t testing.TB) *wallet.EVM {
	t.Helper()
	w, err := wallet.NewEVM()
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// grant adds w to n with role (and resource, when set) as n's owner, and
// returns the raw answer.
func grant(t testing.TB, n *ns.Namespace, w string, role, resource string) *gw.Response {
	t.Helper()
	body := map[string]string{"wallet": w, "role": role}
	if resource != "" {
		body["resource"] = resource
	}
	return send(t, n.Owner.Client, http.MethodPost, pathMembers, n.Owner.Token(), body)
}

// memberToken is a fresh wallet granted role (narrowed to resource) in n and
// signed in to it.
func memberToken(t testing.TB, n *ns.Namespace, role, resource string) (*wallet.EVM, string) {
	t.Helper()
	w := newWallet(t)
	if resp := grant(t, n, w.Address(), role, resource); resp.Status != http.StatusCreated {
		t.Fatalf("granting %s %q: %d %s", role, resource, resp.Status, resp.Body)
	}
	s, err := n.Owner.Client.For(t).SignIn(t.Context(), w, n.Name, nil)
	if err != nil {
		t.Fatal(err)
	}
	return w, s.AccessToken
}

type mintedKey struct {
	ID        int64  `json:"id"`
	APIKey    string `json:"api_key"`
	Scopes    string `json:"scopes"`
	Namespace string `json:"namespace"`
	Label     string `json:"label"`
	ExpiresAt string `json:"expires_at"`
}

// mintKey mints a key in n as its owner; cleanup revokes it (a 404 means a
// test already did).
func mintKey(t testing.TB, n *ns.Namespace, body map[string]any) mintedKey {
	t.Helper()
	var k mintedKey
	resp := send(t, n.Owner.Client, http.MethodPost, pathKeys, n.Owner.Token(), body)
	if err := resp.Expect(t, http.StatusCreated).Decode(&k); err != nil {
		t.Fatal(err)
	}
	protect(t, n.Owner.Client, k.APIKey)
	t.Cleanup(func() { revokeAtCleanup(t, n, k.ID) })
	return k
}

// revokeAtCleanup revokes key id as n's owner; 404 means a test already did.
// The test's own context is cancelled by the time cleanups run.
func revokeAtCleanup(t testing.TB, n *ns.Namespace, id int64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), cleanupBudget)
	defer cancel()
	r, err := n.Owner.Client.Send(ctx, gw.Req{Method: http.MethodDelete, Path: keyPath(id), Bearer: n.Owner.Token()})
	if err != nil || (r.Status != http.StatusOK && r.Status != http.StatusNotFound) {
		t.Errorf("cleanup: failed to revoke key %d: %v", id, err)
	}
}

func keyPath(id int64) string {
	return pathKeys + "/" + strconv.FormatInt(id, 10)
}

// protect registers a credential minted outside the typed gw helpers with
// the evidence redactor.
func protect(t testing.TB, c *gw.Client, values ...string) {
	t.Helper()
	red := c.Recorder().Redactor()
	if red == nil {
		return
	}
	if err := red.Add(values...); err != nil {
		t.Errorf("failed to register minted credentials for redaction: %v", err)
	}
}

func runCLI(t testing.TB, cli *oramacli.Runner, args ...string) oramacli.Result {
	t.Helper()
	res, err := cli.For(t).Run(t.Context(), args...)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func containsFold(s, sub string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(sub))
}
