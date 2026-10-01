package triggers

import (
	"context"
	"errors"
	"testing"

	olriclib "github.com/olric-data/olric"
	"go.uber.org/zap"
)

// Bugboard #30 — cluster-wide once-per-publish dispatch dedup.
//
// gossipsub delivers a publish to every gateway node subscribed to a
// concrete trigger topic, so an N-gateway cluster fired the handler ~N
// times per publish (AnChat: exactly 2 on 3 gateways → 2 pushes/message).
// The dedup claims (namespace, topic, payload-hash) in Olric; only the
// winner dispatches. These tests pin the key derivation (which MUST be
// identical across nodes for the same message) and the fail-open path.

func TestDispatchDedupKey_sameMessageSameKeyAcrossNodes(t *testing.T) {
	// The whole mechanism depends on every node computing the SAME key for
	// the SAME (namespace, topic, payload) — otherwise the cross-node
	// claim can't dedup. Pure function of the inputs, so two "nodes"
	// (two calls) must agree.
	data := []byte(`{"messageId":"abc","seq":42}`)
	k1 := dispatchDedupKey("anchat-test", "messages:new", data)
	k2 := dispatchDedupKey("anchat-test", "messages:new", data)
	if k1 != k2 {
		t.Fatalf("same message must yield same key on every node; got %q vs %q", k1, k2)
	}
	if k1 == "" {
		t.Error("key must not be empty")
	}
}

func TestDispatchDedupKey_differsByPayloadTopicNamespace(t *testing.T) {
	base := dispatchDedupKey("ns", "messages:new", []byte("A"))
	cases := map[string]string{
		"different payload":   dispatchDedupKey("ns", "messages:new", []byte("B")),
		"different topic":     dispatchDedupKey("ns", "other:topic", []byte("A")),
		"different namespace": dispatchDedupKey("ns2", "messages:new", []byte("A")),
	}
	for name, k := range cases {
		if k == base {
			t.Errorf("%s must produce a DIFFERENT key (else distinct events get deduped together)", name)
		}
	}
}

func TestClaimDispatch_failsOpenWhenNoOlric(t *testing.T) {
	// No shared store → can't coordinate → must FIRE (return true), never
	// silently drop the wake. This is the single-node / cache-disabled
	// path and the fail-open guarantee.
	d := &PubSubDispatcher{logger: zap.NewNop()} // olricClient nil
	if !d.claimDispatch(context.Background(), "ns", "messages:new", []byte("x")) {
		t.Error("claimDispatch must fail-open (true) when Olric is unavailable — a dropped wake is worse than a dup")
	}
}

// messageOnlyDMap answers a Put the way a gateway's cluster client does for a
// key another gateway already claimed: an error carrying the text "key found"
// but not the olriclib sentinel, because the client process never registered
// the server's error codes.
type messageOnlyDMap struct {
	olriclib.DMap
	putErr error
}

func (m messageOnlyDMap) Put(context.Context, string, interface{}, ...olriclib.PutOption) error {
	return m.putErr
}

type messageOnlyClient struct {
	olriclib.Client
	dm olriclib.DMap
}

func (c messageOnlyClient) NewDMap(string, ...olriclib.DMapOption) (olriclib.DMap, error) {
	return c.dm, nil
}

func TestClaimDispatch_loserOfTheClusterClaimSkips(t *testing.T) {
	d := &PubSubDispatcher{
		logger:      zap.NewNop(),
		olricClient: messageOnlyClient{dm: messageOnlyDMap{putErr: errors.New("key found")}},
	}
	if d.claimDispatch(context.Background(), "ns", "orders", []byte("x")) {
		t.Error("a node whose claim lost to another node's must skip, got dispatch")
	}
}

func TestClaimDispatch_transientOlricErrorStillFiresOpen(t *testing.T) {
	d := &PubSubDispatcher{
		logger:      zap.NewNop(),
		olricClient: messageOnlyClient{dm: messageOnlyDMap{putErr: errors.New("write quorum cannot be reached")}},
	}
	if !d.claimDispatch(context.Background(), "ns", "orders", []byte("x")) {
		t.Error("an Olric outage must fail open, got skip")
	}
}
