package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
)

// bugboard #733: a signed-in wallet with no grant in a tenant namespace is an
// application's end user, and it could publish to any topic of the namespace.
// It keeps the rest of the data plane and may subscribe, but publishing is for
// a grant that says so, or for the application's functions.

func TestNoGrantWallet_forwardedPublishIsRefusedWithARemedy(t *testing.T) {
	for _, path := range []string{"/v1/pubsub/publish", "/v1/pubsub/publish-batch"} {
		t.Run(path, func(t *testing.T) {
			g, _ := namespaceGatewayForHops(t, "")

			rec, reached := serveHop(g, hop(t, g, http.MethodPost, path, hopNamespace, hopWallet))

			body := rec.Body.String()
			if reached || rec.Code != http.StatusForbidden || !strings.Contains(body, CodeScopeMissing) {
				t.Fatalf("reached %v, status %d, body %s; want 403 %s", reached, rec.Code, body, CodeScopeMissing)
			}
			for _, want := range []string{"pubsub write", "publish from a function", "pubsub:write:*"} {
				if !strings.Contains(body, want) {
					t.Errorf("body %s does not name %q", body, want)
				}
			}
		})
	}
}

func TestNoGrantWallet_directPublishIsRefused(t *testing.T) {
	g, _ := controlPlaneGateway(t, "")
	chain, reached := controlChain(g)

	w := httptest.NewRecorder()
	chain.ServeHTTP(w, grantWalletRequest(http.MethodPost, "/v1/pubsub/publish", "0xstranger", "anchat"))

	// Reached directly, publish asks for ownership, which a wallet with no
	// grant lacks; either refusal keeps it out.
	if *reached || w.Code != http.StatusForbidden {
		t.Fatalf("reached %v, status %d, body %s; want a 403", *reached, w.Code, w.Body.String())
	}
}

func TestNoGrantWallet_keepsSubscribeAndTheRestOfTheDataPlane(t *testing.T) {
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/v1/pubsub/ws"},
		{http.MethodGet, "/v1/pubsub/topics"},
		{http.MethodGet, "/v1/pubsub/presence"},
		{http.MethodPost, "/v1/cache/put"},
		{http.MethodPost, "/v1/storage/upload"},
	} {
		t.Run(route.path, func(t *testing.T) {
			g, _ := namespaceGatewayForHops(t, "")

			rec, reached := serveHop(g, hop(t, g, route.method, route.path, hopNamespace, hopWallet))

			if !reached {
				t.Errorf("a wallet with no grant was refused %s: %d %s", route.path, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestNoGrantWallet_withARuntimeGrantPublishes(t *testing.T) {
	g, _ := namespaceGatewayForHops(t, "runtime")

	rec, reached := serveHop(g, hop(t, g, http.MethodPost, "/v1/pubsub/publish", hopNamespace, hopWallet))

	if !reached {
		t.Errorf("a runtime grant was refused publishing: %d %s", rec.Code, rec.Body.String())
	}
}

func TestNoGrantWallet_ownerAndAdminAndKeysStillPublish(t *testing.T) {
	for _, role := range []string{"developer", "admin", "owner"} {
		g, _ := namespaceGatewayForHops(t, role)
		if rec, reached := serveHop(g, hop(t, g, http.MethodPost, "/v1/pubsub/publish-batch", hopNamespace, hopWallet)); !reached {
			t.Errorf("a %s was refused publishing: %d %s", role, rec.Code, rec.Body.String())
		}
	}
	g, _ := namespaceGatewayForHops(t, "")
	if rec, reached := serveHop(g, keyHop(t, g, http.MethodPost, "/v1/pubsub/publish", "pubsub")); !reached {
		t.Errorf("a pubsub key was refused publishing: %d %s", rec.Code, rec.Body.String())
	}
}

func TestNoGrantWallet_narrowedGrantStillGovernsPublishing(t *testing.T) {
	g, registry := namespaceGatewayForHops(t, "runtime")
	registry.resource = "pubsub:topic=chat.*"

	for topic, want := range map[string]int{
		"chat.general":     http.StatusOK,
		"billing.invoices": http.StatusForbidden,
	} {
		status, reached := serveHopAuthorizing(g, hop(t, g, http.MethodPost, "/v1/pubsub/publish", hopNamespace, hopWallet),
			auth.Resource{Domain: auth.SelectorPubsub, Name: topic, Action: auth.ActionWrite})
		if !reached || status != want {
			t.Errorf("publish to %s: reached %v status %d, want %d", topic, reached, status, want)
		}
	}
}

func TestNoGrantWallet_aReaderIsStillRefusedPublishing(t *testing.T) {
	g, _ := namespaceGatewayForHops(t, "reader")

	rec, reached := serveHop(g, hop(t, g, http.MethodPost, "/v1/pubsub/publish", hopNamespace, hopWallet))

	if reached || rec.Code != http.StatusForbidden {
		t.Errorf("reached %v, status %d; want a reader refused", reached, rec.Code)
	}
	if strings.Contains(rec.Body.String(), "holds no grant") {
		t.Errorf("a reader holds a grant, but was told it holds none: %s", rec.Body.String())
	}
}

func TestNoGrantPermissions_publishIsTheOnlyDataPlaneLoss(t *testing.T) {
	noGrant, full := auth.NoGrantPermissions(), auth.DataPlanePermissions()
	if noGrant.PermitsDomain(auth.DomainPubsub, auth.ActionWrite) {
		t.Error("a wallet with no grant may publish")
	}
	for _, p := range []struct {
		d auth.Domain
		a auth.Action
	}{
		{auth.DomainPubsub, auth.ActionRead}, {auth.DomainStorage, auth.ActionWrite}, {auth.DomainCache, auth.ActionWrite},
		{auth.DomainPush, auth.ActionWrite}, {auth.DomainWebRTC, auth.ActionRead}, {auth.DomainProxy, auth.ActionWrite},
		{auth.DomainFn, auth.ActionInvoke},
	} {
		if !noGrant.PermitsDomain(p.d, p.a) {
			t.Errorf("a wallet with no grant lost %s:%s", p.d, p.a)
		}
		if !full.PermitsDomain(p.d, p.a) {
			t.Errorf("the data plane lacks %s:%s", p.d, p.a)
		}
	}
	if noGrant.PermitsDomain(auth.DomainDB, auth.ActionRead) || noGrant.IsAdmin() {
		t.Error("a wallet with no grant holds more than the data plane")
	}
}
