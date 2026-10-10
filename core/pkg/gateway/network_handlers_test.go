package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/client"
	operatorhandlers "github.com/DeBrosOfficial/network/pkg/gateway/handlers/operator"
	"go.uber.org/zap"
)

const (
	testOperatorWallet = "0xoperator"
	testTenantWallet   = "0xtenant"
)

// fakeNetworkInfo records what connect and disconnect asked of the client.
type fakeNetworkInfo struct {
	client.NetworkInfo
	err      error
	calls    int
	internal bool
}

func (f *fakeNetworkInfo) ConnectToPeer(ctx context.Context, _ string) error {
	f.calls++
	f.internal = client.IsInternalContext(ctx)
	return f.err
}

func (f *fakeNetworkInfo) DisconnectFromPeer(ctx context.Context, _ string) error {
	f.calls++
	f.internal = client.IsInternalContext(ctx)
	return f.err
}

type peerMutationClient struct {
	client.NetworkClient
	info *fakeNetworkInfo
}

func (f *peerMutationClient) Network() client.NetworkInfo { return f.info }

func networkMutationGateway(t *testing.T, info *fakeNetworkInfo) *Gateway {
	t.Helper()
	g, db := registryGateway(t, "index", testOperatorWallet)
	g.operatorHandler = operatorhandlers.NewHandler(zap.NewNop(), db)
	g.client = &peerMutationClient{info: info}
	return g
}

func networkPost(path, wallet, body string) *http.Request {
	r := walletRequest(path, wallet)
	r.Method = http.MethodPost
	r.Body = http.NoBody
	if body != "" {
		r.Body = httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)).Body
	}
	return r
}

type networkHandler func(http.ResponseWriter, *http.Request)

func networkHandlers(g *Gateway) map[string]struct {
	serve networkHandler
	field string
} {
	return map[string]struct {
		serve networkHandler
		field string
	}{
		"/v1/network/connect":    {g.networkConnectHandler, "multiaddr"},
		"/v1/network/disconnect": {g.networkDisconnectHandler, "peer_id"},
	}
}

// A namespace owner holds the operator grant but is not on the operator list:
// refused 403 before the body is looked at, so an empty body is not a 400.
func TestNetworkMutation_nonOperatorIsRefusedBeforeTheBody(t *testing.T) {
	info := &fakeNetworkInfo{}
	g := networkMutationGateway(t, info)
	for path, h := range networkHandlers(g) {
		rec := httptest.NewRecorder()
		h.serve(rec, networkPost(path, testTenantWallet, "{}"))
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "NOT_AN_OPERATOR") {
			t.Errorf("%s as a non-operator: %d %s, want 403 NOT_AN_OPERATOR", path, rec.Code, rec.Body)
		}
	}
	if info.calls != 0 {
		t.Errorf("a refused caller reached the network client %d times", info.calls)
	}
}

func TestNetworkMutation_operatorReachesTheClientWithInternalAuth(t *testing.T) {
	for path := range networkHandlers(nil) {
		info := &fakeNetworkInfo{}
		g := networkMutationGateway(t, info)
		h := networkHandlers(g)[path]
		rec := httptest.NewRecorder()
		h.serve(rec, networkPost(path, testOperatorWallet, fmt.Sprintf(`{"%s":"x"}`, h.field)))
		if rec.Code != http.StatusOK {
			t.Errorf("%s as an operator: %d %s, want 200", path, rec.Code, rec.Body)
		}
		if info.calls != 1 || !info.internal {
			t.Errorf("%s: client called %d times, internal auth %v; the gateway's own client holds no credential", path, info.calls, info.internal)
		}
	}
}

func TestNetworkMutation_malformedBodyIs4xx(t *testing.T) {
	bodies := map[string]string{
		"malformed JSON": `{"x":`,
		"wrong type":     `{"multiaddr":5,"peer_id":5}`,
		"missing field":  `{}`,
		"empty value":    `{"multiaddr":"","peer_id":""}`,
	}
	for path := range networkHandlers(nil) {
		for what, body := range bodies {
			info := &fakeNetworkInfo{}
			g := networkMutationGateway(t, info)
			rec := httptest.NewRecorder()
			networkHandlers(g)[path].serve(rec, networkPost(path, testOperatorWallet, body))
			if rec.Code != http.StatusBadRequest {
				t.Errorf("%s with %s: %d, want 400", path, what, rec.Code)
			}
			if info.calls != 0 {
				t.Errorf("%s with %s reached the client", path, what)
			}
		}
	}
}

func TestNetworkMutation_hugeBodyIs413(t *testing.T) {
	info := &fakeNetworkInfo{}
	g := networkMutationGateway(t, info)
	rec := httptest.NewRecorder()
	g.networkConnectHandler(rec, networkPost("/v1/network/connect", testOperatorWallet,
		`{"multiaddr":"/`+strings.Repeat("a", 2<<20)+`"}`))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("a 2 MiB body: %d, want 413", rec.Code)
	}
}

func TestNetworkMutation_wrongMethodIs405(t *testing.T) {
	g := networkMutationGateway(t, &fakeNetworkInfo{})
	for path, h := range networkHandlers(g) {
		rec := httptest.NewRecorder()
		h.serve(rec, walletRequest(path, testOperatorWallet))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("GET %s: %d, want 405", path, rec.Code)
		}
	}
}

func TestNetworkMutation_clientFailuresAreNotInternalErrors(t *testing.T) {
	cases := map[string]struct {
		err  error
		want int
	}{
		"bad peer":     {fmt.Errorf("%w: nope", client.ErrInvalidPeer), http.StatusBadRequest},
		"dial timeout": {fmt.Errorf("failed to connect to peer: %w", context.DeadlineExceeded), http.StatusGatewayTimeout},
		"dial refused": {errors.New("failed to connect to peer: connection refused"), http.StatusBadGateway},
		"client down":  {client.ErrNotConnected, http.StatusServiceUnavailable},
		"no host":      {client.ErrNoHost, http.StatusServiceUnavailable},
		"no own auth":  {fmt.Errorf("%w: access denied", client.ErrAuthRequired), http.StatusInternalServerError},
	}
	for what, tc := range cases {
		g := networkMutationGateway(t, &fakeNetworkInfo{err: tc.err})
		rec := httptest.NewRecorder()
		g.networkConnectHandler(rec, networkPost("/v1/network/connect", testOperatorWallet, `{"multiaddr":"/ip4/127.0.0.1/tcp/1/p2p/x"}`))
		if rec.Code != tc.want {
			t.Errorf("%s: %d, want %d", what, rec.Code, tc.want)
		}
	}
}
