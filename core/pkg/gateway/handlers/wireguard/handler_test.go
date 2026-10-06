package wireguard

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

// refusingClient fails any query or exec. Every test here asserts that the
// request is refused BEFORE the database is touched, so reaching it is itself
// the failure.
type refusingClient struct {
	// Embedded nil: every method this test does not override panics if the
	// handler reaches for it, which is the same failure signal.
	rqlite.Client
	t *testing.T
}

func (c *refusingClient) Query(context.Context, any, string, ...any) error {
	c.t.Fatal("the request reached the database; it should have been refused first")
	return nil
}

func (c *refusingClient) Exec(context.Context, string, ...any) (sql.Result, error) {
	c.t.Fatal("the request reached the database; it should have been refused first")
	return nil, nil
}

func registerRequest(t *testing.T, remoteAddr, secret string) *http.Request {
	t.Helper()
	body, err := json.Marshal(RegisterPeerRequest{
		NodeID:        "12D3KooWAttacker",
		PublicKey:     "cGVlcgo=",
		PublicIP:      "203.0.113.9",
		ClusterSecret: secret,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/internal/wg/peer", bytes.NewReader(body))
	req.RemoteAddr = remoteAddr
	return req
}

func TestHandleRegisterPeer_refusesACallerOffTheMesh(t *testing.T) {
	h := &Handler{logger: zap.NewNop(), rqliteClient: nil, clusterSecret: "correct-secret"}

	rec := httptest.NewRecorder()
	h.HandleRegisterPeer(rec, registerRequest(t, "198.51.100.7:40000", "correct-secret"))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("a caller from the public internet got %d, want %d — this endpoint writes into the private overlay",
			rec.Code, http.StatusForbidden)
	}
}

func TestHandleRegisterPeer_refusesWhenNoClusterSecretIsConfigured(t *testing.T) {
	// The check used to read `if h.clusterSecret != "" && ...`, so a gateway
	// configured without a secret accepted anything.
	h := &Handler{logger: zap.NewNop(), rqliteClient: &refusingClient{t: t}, clusterSecret: ""}

	rec := httptest.NewRecorder()
	h.HandleRegisterPeer(rec, registerRequest(t, "10.0.0.5:40000", "anything at all"))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d, want %d — with no secret configured there is no way to authenticate the caller",
			rec.Code, http.StatusServiceUnavailable)
	}
}

func TestHandleRegisterPeer_refusesAWrongSecret(t *testing.T) {
	h := &Handler{logger: zap.NewNop(), rqliteClient: &refusingClient{t: t}, clusterSecret: "correct-secret"}

	rec := httptest.NewRecorder()
	h.HandleRegisterPeer(rec, registerRequest(t, "10.0.0.5:40000", "wrong-secret"))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestValidateInternalRequest(t *testing.T) {
	tests := []struct {
		name       string
		secret     string
		remoteAddr string
		header     string
		want       bool
	}{
		{"on the mesh with the right secret", "s3cret", "10.0.0.5:40000", "s3cret", true},
		{"on the mesh with the wrong secret", "s3cret", "10.0.0.5:40000", "nope", false},
		{"on the mesh with no secret sent", "s3cret", "10.0.0.5:40000", "", false},
		{"off the mesh with the right secret", "s3cret", "198.51.100.7:40000", "s3cret", false},
		{"no secret configured", "", "10.0.0.5:40000", "", false},
		{"no secret configured, header supplied", "", "10.0.0.5:40000", "guess", false},
		{"unparseable remote address", "s3cret", "garbage", "s3cret", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := &Handler{logger: zap.NewNop(), clusterSecret: tc.secret}
			req := httptest.NewRequest(http.MethodGet, "/v1/internal/wg/peers", nil)
			req.RemoteAddr = tc.remoteAddr
			if tc.header != "" {
				req.Header.Set("X-Cluster-Secret", tc.header)
			}

			if got := h.validateInternalRequest(req); got != tc.want {
				t.Fatalf("validateInternalRequest = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestHandleRegisterPeer_rejectsWrongMethod(t *testing.T) {
	h := &Handler{logger: zap.NewNop(), rqliteClient: &refusingClient{t: t}, clusterSecret: "s"}
	req := httptest.NewRequest(http.MethodGet, "/v1/internal/wg/peer", nil)
	req.RemoteAddr = "10.0.0.5:40000"

	rec := httptest.NewRecorder()
	h.HandleRegisterPeer(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("got %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleRegisterPeer_rejectsAMalformedNodeID(t *testing.T) {
	h := &Handler{logger: zap.NewNop(), rqliteClient: &refusingClient{t: t}, clusterSecret: "s3cret"}

	req := registerRequest(t, "10.0.0.5:40000", "s3cret") // NodeID is "12D3KooWAttacker"
	rec := httptest.NewRecorder()
	h.HandleRegisterPeer(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want %d — node_id becomes a primary key other code parses as a peer id",
			rec.Code, http.StatusBadRequest)
	}
}
