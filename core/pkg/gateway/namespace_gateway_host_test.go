package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	authsvc "github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/auth/siw"
	authhandlers "github.com/DeBrosOfficial/network/pkg/gateway/handlers/auth"
	"github.com/DeBrosOfficial/network/pkg/logging"
)

func TestNamespaceGatewayHost(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  *Config
		want string
	}{
		{"namespace gateway", &Config{ClientNamespace: "acme", BaseDomain: "orama.example"}, "ns-acme.orama.example"},
		{"cluster gateway", &Config{ClientNamespace: "default", BaseDomain: "orama.example"}, ""},
		{"core gateway", &Config{ClientNamespace: "index", BaseDomain: "orama.example"}, ""},
		{"no client namespace", &Config{BaseDomain: "orama.example"}, ""},
		{"no base domain", &Config{ClientNamespace: "acme", BaseDomain: " "}, ""},
		{"nil config", nil, ""},
	} {
		if got := namespaceGatewayHost(tc.cfg); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

// A namespace gateway built from its config judges a sign-in message by its
// public host, whatever Host the proxy delivered it with.
func TestBindNamespaceSignIn_namespaceGatewayJudgesByItsPublicHost(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  *Config
		want string
	}{
		{"namespace gateway", &Config{ClientNamespace: "acme", BaseDomain: "orama.example"}, authhandlers.ErrCodeSignatureInvalid},
		{"cluster gateway", &Config{ClientNamespace: "default", BaseDomain: "orama.example"}, authhandlers.ErrCodeDomainMismatch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logger, _ := logging.NewColoredLogger(logging.ComponentGateway, false)
			h := authhandlers.NewHandlers(logger, &authsvc.Service{}, nil, ownNamespace(tc.cfg),
				func(ctx context.Context) context.Context { return ctx })
			bindNamespaceSignIn(h, tc.cfg)

			now := time.Now().UTC().Truncate(time.Second)
			m := &siw.Message{
				Chain: siw.Ethereum, Domain: "ns-acme.orama.example",
				Address:   "0xbBbBBBBbbBBBbbbBbbBbbbbBBbBbbbbBbBbbBBbB",
				Statement: "Sign in to the acme namespace on Orama.", URI: "https://ns-acme.orama.example",
				ChainID: "1", Nonce: "0123456789abcdef", IssuedAt: now, ExpirationTime: now.Add(5 * time.Minute),
				Resources: []string{"urn:orama:namespace:acme"},
			}
			message, err := m.Render()
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			raw, _ := json.Marshal(map[string]string{"message": message, "signature": "0xnotasignature"})
			r := httptest.NewRequest(http.MethodPost, "/v1/auth/verify", bytes.NewReader(raw))
			r.Host = "10.0.0.3:10004"
			rec := httptest.NewRecorder()
			h.VerifyHandler(rec, r)

			var body map[string]any
			_ = json.Unmarshal(rec.Body.Bytes(), &body)
			if body["code"] != tc.want {
				t.Errorf("code = %v, want %s", body["code"], tc.want)
			}
		})
	}
}
