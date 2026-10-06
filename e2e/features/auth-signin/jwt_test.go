//go:build e2e_fleet

package authsignin

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

// JWKS routes (docs/API_SURFACE.md "Health and version", "Authentication").
const (
	pathJWKS      = "/v1/auth/jwks"
	pathWellKnown = "/.well-known/jwks.json"
	ed25519Bytes  = 32
	// jwtDots is how many dots make a bearer a JWT to the gateway; any other
	// count is read as an API key (core/pkg/gateway/auth/apikey_request.go).
	jwtDots = 2
	// jwksKeyCacheTTL is how long a gateway caches the published signing keys
	// before reloading them (core/pkg/gateway/auth/signing_keys.go
	// signingKeyReloadInterval).
	jwksKeyCacheTTL = 30 * time.Second
	// jwksReloadBudget waits one cache period plus slack for a namespace
	// gateway's new key to appear in the cluster JWKS.
	jwksReloadBudget = jwksKeyCacheTTL + 15*time.Second
)

// edKid is a gateway signing key id: "ed_" and 16 hex characters of the
// public key's hash (core/pkg/gateway/auth/signing_keys.go).
var edKid = regexp.MustCompile(`^ed_[0-9a-f]{16}$`)

type jwk struct {
	Kty       string  `json:"kty"`
	Alg       string  `json:"alg"`
	Kid       string  `json:"kid"`
	Crv       string  `json:"crv"`
	X         string  `json:"x"`
	Namespace *string `json:"namespace"`
}

func fetchJWKS(t testing.TB, c *gw.Client, path string) map[string]jwk {
	t.Helper()
	var set struct {
		Keys []jwk `json:"keys"`
	}
	if err := c.MustSend(t, gw.Req{Path: path}).Expect(t, http.StatusOK).Decode(&set); err != nil {
		t.Fatal(err)
	}
	out := map[string]jwk{}
	for _, k := range set.Keys {
		out[k.Kid] = k
	}
	return out
}

// TestJWKS_publishesEveryLiveKey: both JWKS routes serve the same keys; every
// EdDSA key carries its kid, curve, 32-byte x and the namespace it is bound
// to; the key that signed a fresh lobby token is there, bound to nothing
// (docs/AUTH.md#which-key-signed-a-token).
func TestJWKS_publishesEveryLiveKey(t *testing.T) {
	t.Parallel()
	c := harness.GW(t)
	a, b := fetchJWKS(t, c, pathJWKS), fetchJWKS(t, c, pathWellKnown)
	if len(a) == 0 || len(a) != len(b) {
		t.Fatalf("%s has %d keys, %s has %d", pathJWKS, len(a), pathWellKnown, len(b))
	}
	for kid, k := range a {
		if _, ok := b[kid]; !ok {
			t.Errorf("kid %s is in %s but not %s", kid, pathJWKS, pathWellKnown)
		}
		if k.Kty != "OKP" {
			continue
		}
		x, err := base64.RawURLEncoding.DecodeString(k.X)
		if !edKid.MatchString(kid) || k.Crv != "Ed25519" || k.Alg != "EdDSA" || err != nil || len(x) != ed25519Bytes || k.Namespace == nil {
			t.Errorf("malformed EdDSA key %+v", k)
		}
	}
	header, _ := jwtClaims(t, signIn(t, c, newWallet(t), "").AccessToken)
	kid, _ := header["kid"].(string)
	k, ok := a[kid]
	if !ok {
		t.Fatalf("the key %q that signed a lobby token is not published", kid)
	}
	if k.Namespace == nil || *k.Namespace != "" {
		t.Errorf("the index gateway's key must be bound to no namespace, it names %v", k.Namespace)
	}
}

// TestJWKS_namespaceGatewayKeyIsBound: a token a namespace gateway signs names
// a key published as bound to that namespace.
func TestJWKS_namespaceGatewayKeyIsBound(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := ns.New(t, f, ns.Options{})
	s := signIn(t, n.Client, n.Owner.Wallet, n.Name)
	header, _ := jwtClaims(t, s.AccessToken)
	kid, _ := header["kid"].(string)
	var k jwk
	eventually.Require(t, pollEvery, jwksReloadBudget, "the namespace gateway's key "+kid+" in the cluster JWKS", func() (bool, error) {
		var ok bool
		k, ok = fetchJWKS(t, harness.GW(t), pathJWKS)[kid]
		if !ok {
			return false, fmt.Errorf("kid %s not published yet", kid)
		}
		return true, nil
	})
	if k.Namespace == nil || *k.Namespace != n.Name {
		t.Fatalf("key %s is bound to %v, want %s", kid, k.Namespace, n.Name)
	}
}

// TestJWT_freshNamespaceTokenAcceptedOnEveryGateway: a token a namespace
// gateway has just signed is accepted at once through every node, not only
// after each gateway's periodic reload of the published keys. A gateway that
// had loaded them before the namespace's key was published answered its
// tokens "no credential was presented" for up to 30 seconds
// (core/pkg/gateway/auth/signing_keys.go signingKeyMissReloadInterval).
func TestJWT_freshNamespaceTokenAcceptedOnEveryGateway(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := ns.New(t, f, ns.Options{})
	s := signIn(t, n.Client, n.Owner.Wallet, n.Name)
	for _, nc := range perNode(t, f, n.Client) {
		if resp := whoami(t, nc.Client, s.AccessToken); resp.Status != http.StatusOK {
			t.Errorf("%s: a fresh namespace token answered %d %s, want 200", nc.Node.Name, resp.Status, resp.ErrorCode())
		}
	}
}

// TestJWT_forgedTokensRefused: a token with no kid, an unknown kid, alg none,
// an edited payload or a cut signature authenticates nothing. A JWT-shaped
// token (exactly two dots) that does not verify is no credential: the gateway
// answers AUTH_MISSING (core/pkg/gateway/middleware.go). Any other shape is
// read as an API key, and no key is spelled like that: AUTH_INVALID_KEY
// (core/pkg/gateway/auth/apikey_request.go APIKeyAndFormFromRequest).
func TestJWT_forgedTokensRefused(t *testing.T) {
	t.Parallel()
	c := harness.GW(t)
	tok := signIn(t, c, newWallet(t), "").AccessToken
	parts := strings.Split(tok, ".")
	header, payload := jwtClaims(t, tok)
	forged := map[string]string{
		"no kid":          reencode(t, without(header, "kid"), payload, parts[2]),
		"unknown kid":     reencode(t, with(header, "kid", "ed_0000000000000000"), payload, parts[2]),
		"alg none":        reencode(t, map[string]any{"alg": "none", "typ": "JWT"}, payload, ""),
		"alg HS256":       reencode(t, with(header, "alg", "HS256"), payload, parts[2]),
		"edited subject":  reencode(t, header, with(payload, "sub", strings.ToLower(newWallet(t).Address())), parts[2]),
		"edited expiry":   reencode(t, header, with(payload, "exp", payload["exp"].(float64)+3600), parts[2]),
		"cut signature":   parts[0] + "." + parts[1] + "." + parts[2][:len(parts[2])/2],
		"two parts":       parts[0] + "." + parts[1],
		"not base64":      "!!!." + parts[1] + "." + parts[2],
		"trailing period": tok + ".",
	}
	for name, bad := range forged {
		want := "AUTH_INVALID_KEY"
		if strings.Count(bad, ".") == jwtDots {
			want = "AUTH_MISSING"
		}
		resp := whoami(t, c, bad)
		if resp.Status != http.StatusUnauthorized || resp.ErrorCode() != want {
			t.Errorf("%s: want 401 %s, got %d %s", name, want, resp.Status, resp.Body)
		}
	}
	whoami(t, c, tok).Expect(t, http.StatusOK)
}

func with(m map[string]any, k string, v any) map[string]any {
	out := map[string]any{}
	for key, val := range m {
		out[key] = val
	}
	out[k] = v
	return out
}

func without(m map[string]any, k string) map[string]any {
	out := with(m, k, nil)
	delete(out, k)
	return out
}

func reencode(t testing.TB, header, payload map[string]any, sig string) string {
	t.Helper()
	enc := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return base64.RawURLEncoding.EncodeToString(b)
	}
	return enc(header) + "." + enc(payload) + "." + sig
}
