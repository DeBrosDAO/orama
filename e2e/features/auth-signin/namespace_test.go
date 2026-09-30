//go:build e2e_fleet

package authsignin

import (
	"net/http"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
	"github.com/DeBrosOfficial/network/e2e/harness/wallet"
)

// pathNamespaces creates a namespace (docs/API_SURFACE.md "Namespace management").
const pathNamespaces = "/v1/namespaces"

// reservedNames are the platform's own labels (core/pkg/gateway/handlers/
// namespace/create_handler.go); a namespace named one is refused.
var reservedNames = []string{"default", "index", "nameserver", "system", "orama", "admin", "internal",
	"api", "www", "mail", "cdn", "docs", "status", "push", "turn", "ns1", "ns2", "ns3", "ns4"}

// invalidNames break `^[a-z0-9][a-z0-9-]{0,38}[a-z0-9]$` after lowercasing.
var invalidNames = []string{"", "a", "-abc", "abc-", "ab_c", "ab.c", "ab c", strings.Repeat("a", 41),
	"ünïcode", "‮abc", "ab\u0000c", "../etc", "' or 1=1--", "ée", "ab/cd", "%2e%2e"}

// createNamespace posts a creation request as bearer.
func createNamespace(t testing.TB, c *gw.Client, bearer, name string) *gw.Response {
	t.Helper()
	return postJSON(t, c, pathNamespaces, bearer, map[string]string{"name": name})
}

// TestNamespaceCreate_nameValidation: every malformed name is refused with
// 400 NAMESPACE_NAME_INVALID before anything is written, in any creation mode.
func TestNamespaceCreate_nameValidation(t *testing.T) {
	t.Parallel()
	c := harness.GW(t)
	tok := signIn(t, c, newWallet(t), "").AccessToken
	for _, name := range invalidNames {
		resp := createNamespace(t, c, tok, name)
		if resp.Status != http.StatusBadRequest || resp.ErrorCode() != "NAMESPACE_NAME_INVALID" {
			t.Errorf("name %q: want 400 NAMESPACE_NAME_INVALID, got %d %.200s", name, resp.Status, resp.Body)
		}
	}
}

// TestNamespaceCreate_reservedNames: the platform's labels cannot be taken.
func TestNamespaceCreate_reservedNames(t *testing.T) {
	t.Parallel()
	c := harness.GW(t)
	tok := signIn(t, c, newWallet(t), "").AccessToken
	for _, name := range append(reservedNames, "DEFAULT", " index ") {
		resp := createNamespace(t, c, tok, name)
		if resp.Status != http.StatusBadRequest || resp.ErrorCode() != "NAMESPACE_NAME_INVALID" ||
			!strings.Contains(string(resp.Body), "reserved") {
			t.Errorf("reserved %q: want 400 NAMESPACE_NAME_INVALID (reserved), got %d %.200s", name, resp.Status, resp.Body)
		}
	}
}

// TestNamespaceCreate_needsASignedInWallet: no token, a garbage token and a
// Solana wallet's session cannot create; neither can a malformed request.
func TestNamespaceCreate_needsASignedInWallet(t *testing.T) {
	t.Parallel()
	c := harness.GW(t)
	name := ns.UniqueName(t.Name())
	if resp := createNamespace(t, c, "", name); resp.Status != http.StatusUnauthorized {
		t.Errorf("no credential: want 401, got %d %s", resp.Status, resp.Body)
	}
	if resp := createNamespace(t, c, "x.y.z", name); resp.Status != http.StatusUnauthorized {
		t.Errorf("garbage token: want 401, got %d %s", resp.Status, resp.Body)
	}
	sol, err := wallet.NewSolana()
	if err != nil {
		t.Fatal(err)
	}
	ch := challengeFor(t, c, gw.ChallengeRequest{Wallet: sol.Address(), ChainType: "SOL"})
	s, _, err := c.For(t).Verify(t.Context(), gw.VerifyRequest{Message: ch.Message, Signature: sol.Sign(ch.Message)})
	if err != nil {
		t.Fatal(err)
	}
	if resp := createNamespace(t, c, s.AccessToken, name); resp.Status != http.StatusUnauthorized {
		t.Errorf("Solana session: want 401 (creation needs an EVM wallet), got %d %s", resp.Status, resp.Body)
	}
	tok := signIn(t, c, newWallet(t), "").AccessToken
	for label, r := range map[string]gw.Req{
		"GET":      {Path: pathNamespaces, Bearer: tok},
		"not JSON": {Method: http.MethodPost, Path: pathNamespaces, Bearer: tok, Body: []byte(`{"name":`)},
	} {
		if resp := c.MustSend(t, r); resp.Status != http.StatusMethodNotAllowed && resp.Status != http.StatusBadRequest {
			t.Errorf("%s: want 405/400, got %d %s", label, resp.Status, resp.Body)
		}
	}
}

// TestNamespaceCreate_takenNameConflicts: a second wallet asking for a name
// that exists gets 409 NAMESPACE_TAKEN and no grant in it.
func TestNamespaceCreate_takenNameConflicts(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := ns.New(t, f, ns.Options{})
	c := harness.GW(t)
	w := newWallet(t)
	ensureCreator(t, w.Address())
	tok := signIn(t, c, w, "").AccessToken
	resp := createNamespace(t, c, tok, n.Name)
	if resp.Status != http.StatusConflict || resp.ErrorCode() != "NAMESPACE_TAKEN" {
		t.Fatalf("want 409 NAMESPACE_TAKEN, got %d %s", resp.Status, resp.Body)
	}
	ch := challengeFor(t, c, gw.ChallengeRequest{Wallet: w.Address(), Namespace: n.Name})
	if r := postJSON(t, c, gw.PathVerify, "", signed(t, w, ch.Message)); r.Status != http.StatusForbidden {
		t.Fatalf("the refused creator can sign in to %s: %d", n.Name, r.Status)
	}
}

// ensureCreator lets wallet create under the cluster's current mode, undoing
// any change: open needs nothing, allowlist needs the wallet listed.
func ensureCreator(t testing.TB, walletAddr string) {
	t.Helper()
	cli := harness.CLI(t)
	out := cli.MustOK(t, "cluster", "settings", "show").Stdout
	switch {
	case strings.Contains(out, "namespace-creation: open"):
	case strings.Contains(out, "namespace-creation: allowlist"):
		cli.MustOK(t, "cluster", "creators", "add", walletAddr)
		t.Cleanup(func() {
			ctx, cancel := fleet.CleanupContext(t)
			defer cancel()
			if res, err := cli.Run(ctx, "cluster", "creators", "remove", walletAddr); err != nil || res.Exit != 0 {
				t.Errorf("cleanup: failed to remove creator %s: %v %s", walletAddr, err, res.Stderr)
			}
		})
	default:
		t.Fatalf("namespace creation admits no user on this cluster (%q); stage 1 leaves it open", out)
	}
}
