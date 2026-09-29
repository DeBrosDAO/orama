package ntfy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/push"
)

func sendTo(p *Provider) error {
	return p.Send(context.Background(), push.PushMessage{DeviceToken: "ns/app/user-1", Title: "t", Body: "b"})
}

// These tests rely on "localhost" resolving to a loopback address, as it does on every supported
// host. A tenant-supplied server is dialed through the guard: a literal internal address and a name that
// resolves to one (here localhost, standing for a name rebound to 10.0.0.x after the config-time
// check) are both refused at the connection, and the server sees nothing.
func TestSend_aTenantServerAtAnInternalAddressIsRefusedAtSendTime(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits++ }))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)

	for name, base := range map[string]string{
		"literal":            srv.URL,
		"name that resolves": "http://localhost:" + u.Port(),
	} {
		err := sendTo(New(Config{BaseURL: base, GuardTarget: true}, nil))
		if err == nil || !strings.Contains(err.Error(), "internal network") {
			t.Errorf("%s: err = %v, want a refused connection", name, err)
		}
	}
	if hits != 0 {
		t.Errorf("the internal server was reached %d times", hits)
	}
}

// A tenant server may not redirect the gateway (a 307 to an internal address would otherwise be
// followed with the request body); the operator's own default keeps the plain client.
func TestNew_aTenantClientRefusesRedirectsAndTheOperatorDefaultIsUnguarded(t *testing.T) {
	tenant := New(Config{BaseURL: "https://ntfy.example.com", GuardTarget: true}, nil)
	req := httptest.NewRequest(http.MethodPost, "http://10.0.0.5/x", nil)
	if err := tenant.httpClient.CheckRedirect(req, nil); err == nil {
		t.Error("a tenant client followed a redirect")
	}
	if operator := New(Config{BaseURL: "http://127.0.0.1:8090"}, nil); operator.httpClient.CheckRedirect != nil {
		t.Error("the operator's default client refuses redirects")
	}
}

// The operator's own default (loopback ntfy) still works.
func TestSend_theOperatorDefaultOnLoopbackStillWorks(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits++ }))
	defer srv.Close()
	if err := sendTo(New(Config{BaseURL: srv.URL}, nil)); err != nil || hits != 1 {
		t.Fatalf("err = %v, hits = %d", err, hits)
	}
}
