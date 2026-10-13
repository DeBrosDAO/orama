package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	authsvc "github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/auth/siw"
)

const (
	nsPublicHost = "ns-acme.orama.example"
	// upstreamHost is what r.Host is on a namespace gateway: the cluster
	// gateway's proxy rewrites it to the WireGuard upstream.
	upstreamHost = "10.0.0.3:10004"
)

func TestOrigin_namespaceGatewayNamesItsPublicHost(t *testing.T) {
	h := NewHandlers(testLogger(), &authsvc.Service{}, nil, "acme", noopInternalAuth)
	h.SetPublicHost(nsPublicHost)
	r := httptest.NewRequest(http.MethodPost, "/v1/auth/challenge", nil)
	r.Host = upstreamHost

	domain, uri, err := h.origin(r)
	if err != nil {
		t.Fatalf("origin: %v", err)
	}
	if domain != nsPublicHost || uri != "https://"+nsPublicHost {
		t.Errorf("origin = %q %q, want the public host, not the upstream address", domain, uri)
	}
}

func TestOrigin_clusterGatewayNamesTheRequestHost(t *testing.T) {
	h := NewHandlers(testLogger(), &authsvc.Service{}, nil, "default", noopInternalAuth)
	r := httptest.NewRequest(http.MethodPost, "/v1/auth/challenge", nil)
	r.Host = "gateway.orama.example"
	r.Header.Set("X-Forwarded-Proto", "https")

	domain, uri, err := h.origin(r)
	if err != nil {
		t.Fatalf("origin: %v", err)
	}
	if domain != "gateway.orama.example" || uri != "https://gateway.orama.example" {
		t.Errorf("origin = %q %q", domain, uri)
	}
}

// A message the client signed for the namespace's public host passes the
// domain check on the namespace gateway, and one naming the upstream address
// does not. The public host comes from configuration, so a forwarded header
// cannot choose it.
func TestVerifyHandler_namespaceGatewayJudgesTheDomainByItsPublicHost(t *testing.T) {
	h := NewHandlers(testLogger(), &authsvc.Service{}, nil, "acme", noopInternalAuth)
	h.SetPublicHost(nsPublicHost)
	now := time.Now().UTC().Truncate(time.Second)

	for _, tc := range []struct {
		domain, code string
	}{
		{nsPublicHost, ErrCodeSignatureInvalid},
		{"10.0.0.3", ErrCodeDomainMismatch},
		{"evil.example", ErrCodeDomainMismatch},
	} {
		r := signInRequest(t, upstreamHost, testMessage(t, tc.domain, now), "0xnotasignature")
		r.Header.Set("X-Forwarded-Host", tc.domain)
		w := httptest.NewRecorder()
		h.VerifyHandler(w, r)

		if code := decodeRefusal(t, w)["code"]; code != tc.code {
			t.Errorf("message for %q: code = %v, want %q", tc.domain, code, tc.code)
		}
	}
}

// namespaceGatewayFlow is the flow fixture as flowNamespace's own gateway,
// reached the way the cluster gateway's proxy reaches it: at the WireGuard
// upstream address.
func namespaceGatewayFlow(t *testing.T) *flow {
	t.Helper()
	f := newFlow(t)
	f.h = NewHandlers(testLogger(), f.svc, nil, flowNamespace, noopInternalAuth)
	f.h.SetPublicHost("ns-" + flowNamespace + ".orama.example")
	return f
}

func (f *flow) atUpstream(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Host = upstreamHost
		handler(w, r)
	}
}

// The stagenet failure end to end: a wallet signs in through
// ns-<namespace>.<base domain>, and the message it signs names that host.
func TestSignIn_throughTheNamespaceGateway(t *testing.T) {
	f := namespaceGatewayFlow(t)
	w := newWallet(t)
	f.member(w.address, authsvc.RoleRuntime)

	code, c := f.do(f.atUpstream(f.h.ChallengeHandler), http.MethodPost, "/v1/auth/challenge",
		map[string]any{"wallet": w.address}, nil)
	if code != http.StatusOK {
		t.Fatalf("challenge: %d %v", code, c)
	}
	message := c["message"].(string)
	m, err := siw.Parse(message)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := m.CheckDomain("ns-" + flowNamespace + ".orama.example"); err != nil {
		t.Fatalf("the client would refuse this message: %v", err)
	}
	if ns, _ := authsvc.NamespaceOf(m); ns != flowNamespace {
		t.Errorf("a challenge naming no namespace is for %q, want the gateway's own %q", ns, flowNamespace)
	}

	code, body := f.do(f.atUpstream(f.h.VerifyHandler), http.MethodPost, "/v1/auth/verify",
		map[string]any{"message": message, "signature": w.sign(message)}, nil)
	if code != http.StatusOK {
		t.Fatalf("verify: %d %v", code, body)
	}
}

func TestChallengeHandler_namespaceGatewayRefusesAnotherNamespace(t *testing.T) {
	f := namespaceGatewayFlow(t)

	code, body := f.do(f.atUpstream(f.h.ChallengeHandler), http.MethodPost, "/v1/auth/challenge",
		map[string]any{"wallet": newWallet(t).address, "namespace": "other"}, nil)

	if code != http.StatusForbidden {
		t.Fatalf("status %d %v, want 403", code, body)
	}
}

// The namespace is read from the signed bytes, so the refusal holds for a
// message the gateway never issued as well.
func TestVerifyHandler_namespaceGatewayRefusesAnotherNamespacesMessage(t *testing.T) {
	f := namespaceGatewayFlow(t)
	w := newWallet(t)
	now := time.Now().UTC().Truncate(time.Second)
	host := "ns-" + flowNamespace + ".orama.example"
	m := &siw.Message{
		Chain: siw.Ethereum, Domain: host, Address: w.address,
		Statement: "Sign in to the other namespace on Orama.", URI: "https://" + host, ChainID: "1",
		Nonce: "0123456789abcdef0123456789abcdef", IssuedAt: now, ExpirationTime: now.Add(5 * time.Minute),
		Resources: []string{"urn:orama:namespace:other"},
	}
	message, err := m.Render()
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	code, body := f.do(f.atUpstream(f.h.VerifyHandler), http.MethodPost, "/v1/auth/verify",
		map[string]any{"message": message, "signature": w.sign(message)}, nil)

	if code != http.StatusUnauthorized || body["code"] != ErrCodeDomainMismatch {
		t.Fatalf("status %d %v, want 401 %s", code, body, ErrCodeDomainMismatch)
	}
}
