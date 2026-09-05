package auth

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	authsvc "github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/auth/siw"
)

// A refusal that says only "signature verification failed" tells a developer
// nothing about which of six causes it was, so every one of them carries a code
// and these check the code is the right one. The wire values are the contract:
// the SDK switches on them.

func signInRequest(t *testing.T, host, message, signature string) *http.Request {
	t.Helper()
	body, err := json.Marshal(map[string]string{"message": message, "signature": signature})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	r := httptest.NewRequest(http.MethodPost, "/v1/auth/verify", bytes.NewReader(body))
	r.Host = host
	return r
}

func decodeRefusal(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode refusal: %v", err)
	}
	return body
}

func testMessage(t *testing.T, domain string, issuedAt time.Time) string {
	t.Helper()
	m := &siw.Message{
		Chain:          siw.Ethereum,
		Domain:         domain,
		Address:        "0xbBbBBBBbbBBBbbbBbbBbbbbBBbBbbbbBbBbbBBbB",
		Statement:      "Sign in to the acme namespace on Orama.",
		URI:            "https://" + domain,
		ChainID:        "1",
		Nonce:          "0123456789abcdef",
		IssuedAt:       issuedAt,
		ExpirationTime: issuedAt.Add(5 * time.Minute),
		Resources:      []string{"urn:orama:namespace:acme"},
	}
	text, err := m.Render()
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	return text
}

func TestVerifyHandler_refusalsCarryACode(t *testing.T) {
	h := NewHandlers(testLogger(), &authsvc.Service{}, nil, "default", noopInternalAuth)
	now := time.Now().UTC().Truncate(time.Second)

	tests := []struct {
		name    string
		host    string
		message string
		code    string
	}{
		{
			name:    "a message signed for another site",
			host:    "gateway.example",
			message: testMessage(t, "evil.example", now),
			code:    ErrCodeDomainMismatch,
		},
		{
			name:    "a message whose own deadline has passed",
			host:    "gateway.example",
			message: testMessage(t, "gateway.example", now.Add(-time.Hour)),
			code:    ErrCodeMessageExpired,
		},
		{
			name:    "something that is not a sign-in message",
			host:    "gateway.example",
			message: "0123456789abcdef",
			code:    ErrCodeMessageMalformed,
		},
		{
			name:    "a real message with a signature that is not one",
			host:    "gateway.example",
			message: testMessage(t, "gateway.example", now),
			code:    ErrCodeSignatureInvalid,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			h.VerifyHandler(w, signInRequest(t, tc.host, tc.message, "0xnotasignature"))

			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status %d, want 401", w.Code)
			}
			body := decodeRefusal(t, w)
			if body["code"] != tc.code {
				t.Errorf("code = %v, want %q", body["code"], tc.code)
			}
			if body["hint"] == "" || body["hint"] == nil {
				t.Error("the refusal carries no hint, so it says what went wrong and not what to do")
			}
			if w.Header().Get("WWW-Authenticate") == "" {
				t.Error("a 401 with no WWW-Authenticate")
			}
		})
	}
}

// The old body was {wallet, nonce, signature}. A client sending it now gets a
// 400 that says what to send instead, rather than a 401 it will read as a bad
// signature and retry forever.
func TestVerifyHandler_theOldRequestShapeIsAClearRefusal(t *testing.T) {
	h := NewHandlers(testLogger(), &authsvc.Service{}, nil, "default", noopInternalAuth)

	body, err := json.Marshal(map[string]string{
		"wallet": "0xbBbBBBBbbBBBbbbBbbBbbbbBBbBbbbbBbBbbBBbB",
		"nonce":  "0123456789abcdef",
		// no message
		"signature": "0xsig",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	r := httptest.NewRequest(http.MethodPost, "/v1/auth/verify", bytes.NewReader(body))
	r.Host = "gateway.example"

	w := httptest.NewRecorder()
	h.VerifyHandler(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", w.Code)
	}
}

// The api-key endpoint mints the same credential from the same signature, so it
// refuses on the same terms.
func TestIssueAPIKeyHandler_refusesAMessageSignedForAnotherSite(t *testing.T) {
	h := NewHandlers(testLogger(), &authsvc.Service{}, nil, "default", noopInternalAuth)

	r := signInRequest(t, "gateway.example",
		testMessage(t, "evil.example", time.Now().UTC().Truncate(time.Second)), "0xsig")
	w := httptest.NewRecorder()
	h.IssueAPIKeyHandler(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", w.Code)
	}
	if code := decodeRefusal(t, w)["code"]; code != ErrCodeDomainMismatch {
		t.Errorf("code = %v, want %q", code, ErrCodeDomainMismatch)
	}
}

func TestRequestOrigin(t *testing.T) {
	tests := []struct {
		name       string
		host       string
		forwarded  string
		tls        bool
		wantDomain string
		wantURI    string
	}{
		{"behind caddy", "gateway.orama.network", "https", false, "gateway.orama.network", "https://gateway.orama.network"},
		{"direct tls", "gateway.orama.network", "", true, "gateway.orama.network", "https://gateway.orama.network"},
		{"plain http", "localhost:6001", "", false, "localhost", "http://localhost:6001"},
		{"ipv6 with a port", "[::1]:6001", "", false, "::1", "http://[::1]:6001"},
		{"ipv6 without a port", "[::1]", "", false, "::1", "http://[::1]"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/v1/auth/challenge", nil)
			r.Host = tc.host
			if tc.forwarded != "" {
				r.Header.Set("X-Forwarded-Proto", tc.forwarded)
			}
			if tc.tls {
				r.TLS = &tlsState
			}

			domain, uri, err := requestOrigin(r)
			if err != nil {
				t.Fatalf("requestOrigin: %v", err)
			}
			if domain != tc.wantDomain {
				t.Errorf("domain = %q, want %q", domain, tc.wantDomain)
			}
			if uri != tc.wantURI {
				t.Errorf("uri = %q, want %q", uri, tc.wantURI)
			}
		})
	}
}

// A request with no Host has no domain to name, and a message with a domain
// this gateway invented would be one no wallet could check against the site the
// user is looking at.
func TestRequestOrigin_refusesARequestWithNoHost(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/v1/auth/challenge", nil)
	r.Host = ""
	if _, _, err := requestOrigin(r); err == nil {
		t.Fatal("a request with no Host produced a domain")
	}
}

// tlsState stands in for a terminated TLS connection; only its presence matters.
var tlsState = tls.ConnectionState{}

// The message carries the EIP-55 checksummed address, because the grammar
// requires it. Everything the gateway keys on — namespace_ownership, api_keys,
// the nonce row — stores the normalised form, and two spellings of one wallet
// is how an owner stops being an owner.
//
// This reads the source rather than the behaviour because the seam is inside a
// method that needs a live registry to reach; what it protects is the one line
// that does the conversion.
func TestSignIn_normalisesTheWalletBeforeAnythingIsKeyedOnIt(t *testing.T) {
	src, err := os.ReadFile("signin.go")
	if err != nil {
		t.Fatalf("read signin.go: %v", err)
	}
	if !strings.Contains(string(src), "authsvc.NormalizeWallet(m.Address)") {
		t.Error("signIn returns the address as the message spells it. The message is EIP-55 " +
			"checksummed and every table is keyed on the normalised form, so an owner signing " +
			"in stops matching their own ownership row.")
	}
}
