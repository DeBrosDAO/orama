package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	nodeauth "github.com/DeBrosOfficial/network/pkg/auth"
	"github.com/DeBrosOfficial/network/pkg/constants"
)

// Bugboard #858 — the fan-out resolver turns active dns_nodes into overlay
// targets and caches them for a short TTL. These pin the transform + caching.

func nodesQuery(nodes ...ntfyFanoutNode) func(context.Context) ([]ntfyFanoutNode, error) {
	return func(context.Context) ([]ntfyFanoutNode, error) { return nodes, nil }
}

func TestNtfyFanoutResolver_targetsAreOverlayGateways(t *testing.T) {
	r := &ntfyFanoutResolver{
		port: constants.GatewayAPIPort,
		ttl:  time.Minute,
		query: nodesQuery(
			ntfyFanoutNode{ID: "peer-a", InternalIP: "10.0.0.1"},
			ntfyFanoutNode{ID: "peer-b", InternalIP: "10.0.0.2"},
		),
	}
	targets, err := r.Targets(context.Background())
	if err != nil {
		t.Fatalf("Targets: %v", err)
	}
	if len(targets) != 2 {
		t.Fatalf("got %d targets; want 2", len(targets))
	}
	want := map[string]string{"peer-a": "http://10.0.0.1:10104", "peer-b": "http://10.0.0.2:10104"}
	for _, tg := range targets {
		if want[tg.NodeID] != tg.BaseURL {
			t.Errorf("target %s = %q; want %q", tg.NodeID, tg.BaseURL, want[tg.NodeID])
		}
	}
}

func TestNtfyFanoutResolver_noNodes_returnsEmpty(t *testing.T) {
	r := &ntfyFanoutResolver{port: constants.GatewayAPIPort, ttl: time.Minute, query: nodesQuery()}
	targets, err := r.Targets(context.Background())
	if err != nil {
		t.Fatalf("Targets: %v", err)
	}
	if len(targets) != 0 {
		t.Errorf("got %v; want no targets", targets)
	}
}

func TestNtfyFanoutResolver_cachesWithinTTL(t *testing.T) {
	calls := 0
	r := &ntfyFanoutResolver{
		port: constants.GatewayAPIPort,
		ttl:  time.Minute,
		query: func(context.Context) ([]ntfyFanoutNode, error) {
			calls++
			return []ntfyFanoutNode{{ID: "p", InternalIP: "10.0.0.1"}}, nil
		},
	}
	for i := 0; i < 3; i++ {
		if _, err := r.Targets(context.Background()); err != nil {
			t.Fatalf("Targets: %v", err)
		}
	}
	if calls != 1 {
		t.Errorf("query ran %d times within the TTL; want 1", calls)
	}
}

func TestNtfyFanoutResolver_requeriesAfterTTL(t *testing.T) {
	calls := 0
	r := &ntfyFanoutResolver{
		port: constants.GatewayAPIPort,
		ttl:  time.Millisecond,
		query: func(context.Context) ([]ntfyFanoutNode, error) {
			calls++
			return []ntfyFanoutNode{{ID: "p", InternalIP: "10.0.0.1"}}, nil
		},
	}
	if _, err := r.Targets(context.Background()); err != nil {
		t.Fatalf("Targets: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	if _, err := r.Targets(context.Background()); err != nil {
		t.Fatalf("Targets: %v", err)
	}
	if calls != 2 {
		t.Errorf("query ran %d times across the TTL; want 2", calls)
	}
}

func TestNtfyFanoutResolver_queryError_isReturnedNotMasked(t *testing.T) {
	boom := errors.New("rqlite down")
	r := &ntfyFanoutResolver{
		port:  constants.GatewayAPIPort,
		ttl:   time.Minute,
		query: func(context.Context) ([]ntfyFanoutNode, error) { return nil, boom },
	}
	targets, err := r.Targets(context.Background())
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v; want the query error", err)
	}
	if targets != nil {
		t.Errorf("got targets %v on error; want none", targets)
	}
}

// laterNow is a clock past the process's first second, so a stamp is not
// refused as possibly older than the process.
func laterNow() time.Time { return time.Now().Add(testStampLead) }

func TestNtfyFanoutSigner_stampsForTheNamedNode(t *testing.T) {
	const secret = "a cluster secret"
	sign := newNtfyFanoutSigner(secret, laterNow)
	key, _ := nodeauth.CoordinationKey(secret)

	r := httptest.NewRequest(http.MethodPost, "/v1/internal/push/ntfy/topic1", strings.NewReader("hello"))
	if err := sign(r, coordinationTestNode); err != nil {
		t.Fatalf("sign: %v", err)
	}
	if !nodeauth.VerifyCoordinationV2(key, r, time.Now().Add(testStampLead), coordinationTestNode) {
		t.Error("the stamp does not verify for the node it was signed for")
	}
	other := httptest.NewRequest(http.MethodPost, "/v1/internal/push/ntfy/topic1", strings.NewReader("hello"))
	if err := sign(other, coordinationTestNode); err != nil {
		t.Fatalf("sign: %v", err)
	}
	if nodeauth.VerifyCoordinationV2(key, other, time.Now().Add(testStampLead), "12D3KooWSomeOtherNode") {
		t.Error("the stamp verifies at a node it was not signed for")
	}
}

func TestNtfyFanoutSigner_noClusterSecret_fails(t *testing.T) {
	sign := newNtfyFanoutSigner("", laterNow)
	r := httptest.NewRequest(http.MethodPost, "/v1/internal/push/ntfy/topic1", strings.NewReader("hello"))
	if err := sign(r, coordinationTestNode); err == nil {
		t.Fatal("signing without a cluster secret succeeded")
	}
}

func TestNtfyFanoutSigner_noAudience_fails(t *testing.T) {
	sign := newNtfyFanoutSigner("a cluster secret", laterNow)
	r := httptest.NewRequest(http.MethodPost, "/v1/internal/push/ntfy/topic1", strings.NewReader("hello"))
	if err := sign(r, ""); err == nil {
		t.Fatal("signing without a node to address succeeded")
	}
}
