package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	nodeauth "github.com/DeBrosOfficial/network/pkg/auth"
)

// The repair endpoint stops a namespace's services and starts them again. It
// was reachable by anything on the WireGuard overlay that knew a constant
// printed in this repository — which is every namespace's own workloads.

func coordinationGateway(secret string) *Gateway {
	return &Gateway{cfg: &Config{ClusterSecret: secret}}
}

func meshRequest(method, target string) *http.Request {
	r := httptest.NewRequest(method, target, nil)
	r.RemoteAddr = "10.0.0.7:41000"
	return r
}

func TestVerifyCoordination_acceptsASignedRequestFromTheMesh(t *testing.T) {
	g := coordinationGateway("a cluster secret")
	key, err := nodeauth.CoordinationKey("a cluster secret")
	if err != nil {
		t.Fatalf("derive: %v", err)
	}

	r := meshRequest(http.MethodPost, "/v1/internal/namespace/repair?namespace=acme")
	if err := nodeauth.SignCoordination(key, r, time.Now()); err != nil {
		t.Fatalf("sign: %v", err)
	}
	if !g.verifyCoordination(r) {
		t.Fatal("a signed request from the mesh was refused")
	}
}

func TestVerifyCoordination_refuses(t *testing.T) {
	g := coordinationGateway("a cluster secret")
	key, _ := nodeauth.CoordinationKey("a cluster secret")

	sign := func(r *http.Request) *http.Request {
		if err := nodeauth.SignCoordination(key, r, time.Now()); err != nil {
			t.Fatalf("sign: %v", err)
		}
		return r
	}

	t.Run("the old constant, which is in this source", func(t *testing.T) {
		r := meshRequest(http.MethodPost, "/v1/internal/namespace/repair?namespace=acme")
		r.Header.Set("X-Orama-Internal-Auth", "namespace-coordination")
		if g.verifyCoordination(r) {
			t.Error("the constant still authenticates a coordination request")
		}
	})

	t.Run("a signed request from off the mesh", func(t *testing.T) {
		r := sign(httptest.NewRequest(http.MethodPost, "/v1/internal/namespace/repair", nil))
		r.RemoteAddr = "203.0.113.9:41000"
		if g.verifyCoordination(r) {
			t.Error("a request from a public address was accepted")
		}
	})

	t.Run("a gateway with no cluster secret", func(t *testing.T) {
		r := sign(meshRequest(http.MethodPost, "/v1/internal/namespace/repair"))
		if coordinationGateway("").verifyCoordination(r) {
			t.Error("a gateway with no cluster secret accepted a coordination request; " +
				"it has no way to check one, so it must refuse")
		}
	})

	t.Run("no configuration at all", func(t *testing.T) {
		r := sign(meshRequest(http.MethodPost, "/v1/internal/namespace/repair"))
		if (&Gateway{}).verifyCoordination(r) {
			t.Error("an unconfigured gateway accepted a coordination request")
		}
	})
}

// The repair handler is the one that stops and restarts a namespace's services,
// so the check has to be in the chain rather than merely written.
func TestNamespaceClusterRepairHandler_refusesAnUnsignedRequest(t *testing.T) {
	g := coordinationGateway("a cluster secret")

	w := httptest.NewRecorder()
	r := meshRequest(http.MethodPost, "/v1/internal/namespace/repair?namespace=acme")
	r.Header.Set("X-Orama-Internal-Auth", "namespace-coordination")
	g.namespaceClusterRepairHandler(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status %d, want 401", w.Code)
	}
}
