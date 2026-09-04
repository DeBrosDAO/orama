package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/secrets"
)

// The header this replaces was `X-Orama-Internal-Auth: namespace-coordination`
// — a constant in this repository — guarded only by the source address being on
// the WireGuard overlay. Every namespace's services are on that mesh, so any
// tenant workload that could reach a node's gateway port could spawn or stop
// services for any namespace on it.

func coordinationRequest(method, url string) *http.Request {
	return httptest.NewRequest(method, url, nil)
}

func TestCoordination_signAndVerify(t *testing.T) {
	key, err := CoordinationKey("a cluster secret")
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	now := time.Now()

	r := coordinationRequest(http.MethodPost, "/v1/internal/namespace/spawn?namespace=acme")
	if err := SignCoordination(key, r, now); err != nil {
		t.Fatalf("sign: %v", err)
	}
	if !VerifyCoordination(key, r, now) {
		t.Fatal("a request this node signed did not verify")
	}
}

func TestCoordination_refuses(t *testing.T) {
	key, _ := CoordinationKey("a cluster secret")
	other, _ := CoordinationKey("a different cluster")
	now := time.Now()

	signed := func() *http.Request {
		r := coordinationRequest(http.MethodPost, "/v1/internal/namespace/spawn?namespace=acme")
		if err := SignCoordination(key, r, now); err != nil {
			t.Fatalf("sign: %v", err)
		}
		return r
	}

	t.Run("no stamp at all — which is what the old constant amounted to", func(t *testing.T) {
		r := coordinationRequest(http.MethodPost, "/v1/internal/namespace/spawn?namespace=acme")
		r.Header.Set("X-Orama-Internal-Auth", "namespace-coordination")
		if VerifyCoordination(key, r, now) {
			t.Error("an unsigned request verified")
		}
	})

	t.Run("another cluster's secret", func(t *testing.T) {
		if VerifyCoordination(other, signed(), now) {
			t.Error("a stamp from one cluster verified in another")
		}
	})

	t.Run("replayed onto a different namespace", func(t *testing.T) {
		r := signed()
		r.URL.RawQuery = "namespace=victim"
		if VerifyCoordination(key, r, now) {
			t.Error("a stamp for one namespace verified for another — the query is what carries it")
		}
	})

	t.Run("replayed onto a different path", func(t *testing.T) {
		r := signed()
		r.URL.Path = "/v1/internal/namespace/repair"
		if VerifyCoordination(key, r, now) {
			t.Error("a stamp for one route verified on another")
		}
	})

	t.Run("replayed onto a different method", func(t *testing.T) {
		r := signed()
		r.Method = http.MethodDelete
		if VerifyCoordination(key, r, now) {
			t.Error("a stamp made on a POST verified on a DELETE")
		}
	})

	t.Run("an old stamp", func(t *testing.T) {
		if VerifyCoordination(key, signed(), now.Add(2*coordinationMaxSkew)) {
			t.Error("a stale stamp verified; a captured request would be replayable forever")
		}
	})

	t.Run("a stamp from the future", func(t *testing.T) {
		if VerifyCoordination(key, signed(), now.Add(-2*coordinationMaxSkew)) {
			t.Error("a stamp from the future verified; it is as much a sign of forgery as an old one")
		}
	})

	t.Run("a malformed stamp", func(t *testing.T) {
		for _, value := range []string{"", "nonsense", "123", "123.", ".abcd", "abc.def", "999999999999999999999.aa"} {
			r := coordinationRequest(http.MethodPost, "/v1/internal/namespace/spawn?namespace=acme")
			r.Header.Set(CoordinationMACHeader, value)
			if VerifyCoordination(key, r, now) {
				t.Errorf("the stamp %q verified", value)
			}
		}
	})

	t.Run("no key on the verifying side", func(t *testing.T) {
		if VerifyCoordination(nil, signed(), now) {
			t.Error("a node with no cluster secret accepted a coordination request")
		}
	})
}

// A node with no cluster secret cannot sign, and must say so rather than
// sending an unsigned request that the other end will refuse for a reason
// nobody can see.
func TestCoordination_refusesToSignWithoutASecret(t *testing.T) {
	for _, secret := range []string{"", "   ", "\n"} {
		if _, err := CoordinationKey(secret); err == nil {
			t.Errorf("a key was derived from %q", secret)
		}
	}
	r := coordinationRequest(http.MethodPost, "/v1/internal/namespace/spawn")
	if err := SignCoordination(nil, r, time.Now()); err == nil {
		t.Error("a request was signed with no key")
	}
	if r.Header.Get(CoordinationMACHeader) != "" {
		t.Error("a stamp was set despite the failure")
	}
}

// The key is domain-separated from every other key derived from the same
// cluster secret, so a MAC from one is not usable as another.
func TestCoordinationKey_isNotTheInternalAuthHopKey(t *testing.T) {
	coordination, err := CoordinationKey("a cluster secret")
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	for _, purpose := range []string{"internal-auth-hop", "turn-encryption", "orama-secrets-encryption-v1"} {
		other, err := secrets.DeriveKey("a cluster secret", purpose)
		if err != nil {
			t.Fatalf("derive %s: %v", purpose, err)
		}
		if string(coordination) == string(other) {
			t.Errorf("the coordination key is the same as the %q key", purpose)
		}
	}
}

// The secret is read from a file on each node. One copy having a trailing
// newline and another not must not derive two different keys: every
// coordination call between those nodes would then fail a MAC check for no
// visible reason, which is how get_secret stayed broken for days.
func TestCoordinationKey_ignoresSurroundingWhitespace(t *testing.T) {
	canonical, err := CoordinationKey("a cluster secret")
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	for _, spelling := range []string{"a cluster secret\n", "  a cluster secret", "a cluster secret\r\n"} {
		got, err := CoordinationKey(spelling)
		if err != nil {
			t.Fatalf("derive %q: %v", spelling, err)
		}
		if string(got) != string(canonical) {
			t.Errorf("%q derives a different key from the same secret written plainly", spelling)
		}
	}
}
