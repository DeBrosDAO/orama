package triggers

import (
	"context"
	"fmt"
	"testing"

	olriclib "github.com/olric-data/olric"
	"github.com/olric-data/olric/stats"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// failingOlricClient is a minimal olric.Client whose NewDMap always errors,
// simulating an Olric backend that is configured but unavailable — the
// degraded path bugboard #555 must surface (fail-open + rate-limited WARN).
type failingOlricClient struct{}

func (failingOlricClient) NewDMap(string, ...olriclib.DMapOption) (olriclib.DMap, error) {
	return nil, fmt.Errorf("olric unavailable (test)")
}
func (failingOlricClient) NewPubSub(...olriclib.PubSubOption) (*olriclib.PubSub, error) {
	return nil, fmt.Errorf("not implemented")
}
func (failingOlricClient) Stats(context.Context, string, ...olriclib.StatsOption) (stats.Stats, error) {
	return stats.Stats{}, fmt.Errorf("not implemented")
}
func (failingOlricClient) Ping(context.Context, string, string) (string, error) {
	return "", fmt.Errorf("not implemented")
}
func (failingOlricClient) RoutingTable(context.Context) (olriclib.RoutingTable, error) {
	return nil, fmt.Errorf("not implemented")
}
func (failingOlricClient) Members(context.Context) ([]olriclib.Member, error) {
	return nil, fmt.Errorf("not implemented")
}
func (failingOlricClient) RefreshMetadata(context.Context) error { return nil }
func (failingOlricClient) Close(context.Context) error           { return nil }

var _ olriclib.Client = failingOlricClient{}

// Bugboard #555 — duplicate push from the dispatcher firing twice.
//
// These exercise Dispatch's local-dedup short-circuit and the
// degraded-dedup WARN. They use a nil-db store: getMatches would panic on
// the nil rqlite.Client, so "did we reach getMatches?" is observable as
// "did Dispatch panic?". The local dedup runs BEFORE getMatches, so a
// deduped call must return cleanly without touching the store.

func TestDispatch_localDedupSkipsSecondInvokeSameNode(t *testing.T) {
	logger := zap.NewNop()
	store := NewPubSubTriggerStore(nil, logger) // nil db: getMatches panics if reached
	d := NewPubSubDispatcher(store, nil, nil, nil, logger)

	ns, topic, data := "anchat", "messages:new", []byte(`{"messageId":"m1"}`)

	// First publish: NOT deduped → reaches getMatches → nil-db panic. We
	// recover and confirm we got past the dedup gate.
	reachedStore := false
	func() {
		defer func() {
			if recover() != nil {
				reachedStore = true
			}
		}()
		d.Dispatch(context.Background(), ns, topic, data, 0)
	}()
	if !reachedStore {
		t.Fatal("first publish must pass the dedup gate and reach the store lookup")
	}

	// Second IDENTICAL publish within the TTL: MUST be deduped locally and
	// return BEFORE getMatches — so no panic this time.
	dedupedClean := true
	func() {
		defer func() {
			if recover() != nil {
				dedupedClean = false
			}
		}()
		d.Dispatch(context.Background(), ns, topic, data, 0)
	}()
	if !dedupedClean {
		t.Error("BUG #555 REGRESSION: identical second publish on the same node " +
			"must be deduped locally and NOT re-dispatch")
	}
}

func TestDispatch_distinctPayloadsBothDispatch(t *testing.T) {
	logger := zap.NewNop()
	store := NewPubSubTriggerStore(nil, logger)
	d := NewPubSubDispatcher(store, nil, nil, nil, logger)

	ns, topic := "anchat", "messages:new"

	for _, data := range [][]byte{[]byte(`{"messageId":"a"}`), []byte(`{"messageId":"b"}`)} {
		reachedStore := false
		func() {
			defer func() {
				if recover() != nil {
					reachedStore = true
				}
			}()
			d.Dispatch(context.Background(), ns, topic, data, 0)
		}()
		if !reachedStore {
			t.Errorf("distinct payload %q must NOT be deduped — it must reach dispatch", data)
		}
	}
}

func TestClaimDispatch_degradedWarnWhenOlricDown(t *testing.T) {
	// Olric "configured but failing" path: a non-nil client whose NewDMap
	// errors. claimDispatch must STILL fire (fail-open) AND emit a WARN so
	// operators can see cross-node dedup is degraded.
	core, observed := observer.New(zapcore.WarnLevel)
	d := &PubSubDispatcher{
		logger:      zap.New(core),
		olricClient: failingOlricClient{},
	}

	if !d.claimDispatch(context.Background(), "ns", "messages:new", []byte("x")) {
		t.Fatal("claimDispatch must fail-open (true) when Olric is degraded — never drop the wake")
	}
	if observed.FilterMessageSnippet("dedup degraded").Len() == 0 {
		t.Error("degraded Olric path must emit a WARN naming the degradation, not stay silent")
	}
}

func TestClaimDispatch_degradedWarnRateLimited(t *testing.T) {
	// A sustained outage must NOT flood the log: only one WARN per interval.
	core, observed := observer.New(zapcore.WarnLevel)
	d := &PubSubDispatcher{
		logger:      zap.New(core),
		olricClient: failingOlricClient{},
	}

	for i := 0; i < 5; i++ {
		d.claimDispatch(context.Background(), "ns", "messages:new", []byte("x"))
	}
	if got := observed.FilterMessageSnippet("dedup degraded").Len(); got != 1 {
		t.Errorf("degraded WARN must be rate-limited to 1 per interval; got %d", got)
	}
}

func TestClaimDispatch_nilOlricStaysQuiet(t *testing.T) {
	// nil Olric is a NORMAL single-node / cache-disabled config, not a
	// degraded multi-node cluster. It must fire but NOT warn (avoid noise).
	core, observed := observer.New(zapcore.WarnLevel)
	d := &PubSubDispatcher{logger: zap.New(core)} // olricClient nil

	if !d.claimDispatch(context.Background(), "ns", "messages:new", []byte("x")) {
		t.Fatal("nil Olric must fail-open (true)")
	}
	if observed.Len() != 0 {
		t.Errorf("nil Olric is a normal config and must NOT emit a degraded WARN; got %d logs", observed.Len())
	}
}
