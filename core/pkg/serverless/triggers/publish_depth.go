package triggers

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/DeBrosOfficial/network/pkg/olric"
	olriclib "github.com/olric-data/olric"
	"go.uber.org/zap"
)

// A publish made from inside a triggered function reaches the dispatcher a
// second time as a libp2p message, and the libp2p wire format carries nothing
// but the payload: the trigger depth the function ran at was gone, so every
// handler that republished started its chain over at depth 0 and
// maxTriggerDepth never engaged.
//
// The depth now travels beside the message instead of inside it. Before it
// publishes, the host records the publishing invocation's depth under the
// message's dispatch key (namespace, topic, payload hash — the key the
// once-per-publish dedup already uses), on this gateway and, when the
// namespace has Olric, cluster-wide so whichever gateway wins the dispatch
// claim reads it back. The payload is untouched, so no subscriber sees an
// envelope.
//
// Only a function invocation can write a record, and a record only ever raises
// the depth a message is dispatched at: a message nothing recorded — anything
// a client publishes — is dispatched at depth 0, and a tenant cannot lower the
// depth of a function's publish because nothing it controls writes the entry.
const (
	// publishDepthDMap holds the cluster-wide depth records.
	publishDepthDMap = "pubsub_publish_depth"

	// publishDepthTTL is how long a record is kept: the dedup window, because
	// a message can only be dispatched once inside it.
	publishDepthTTL = dispatchDedupTTL

	// localDepthMaxEntries caps the gateway's own record map. Past the cap a
	// publish that needs a record is refused (ErrPublishDepthFull) rather than
	// dispatched without its depth.
	localDepthMaxEntries = 65536
)

// ErrPublishDepthFull is returned by RecordPublishDepth when the gateway holds
// localDepthMaxEntries unexpired records.
var ErrPublishDepthFull = errors.New("publish depth ledger is full")

// depthStore is the cluster-wide half of the ledger.
type depthStore interface {
	put(ctx context.Context, key string, depth int) error
	// get returns the recorded depth, or ok=false when nothing is recorded.
	get(ctx context.Context, key string) (depth int, ok bool, err error)
}

// olricDepthStore keeps the records in a per-namespace Olric DMap.
type olricDepthStore struct{ client olriclib.Client }

func (s olricDepthStore) put(ctx context.Context, key string, depth int) error {
	dm, err := s.client.NewDMap(publishDepthDMap)
	if err != nil {
		return fmt.Errorf("failed to open DMap %s: %w", publishDepthDMap, err)
	}
	return dm.Put(ctx, key, depth, olriclib.EX(publishDepthTTL))
}

func (s olricDepthStore) get(ctx context.Context, key string) (int, bool, error) {
	dm, err := s.client.NewDMap(publishDepthDMap)
	if err != nil {
		return 0, false, fmt.Errorf("failed to open DMap %s: %w", publishDepthDMap, err)
	}
	gr, err := dm.Get(ctx, key)
	if olric.IsKeyNotFound(err) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	depth, err := gr.Int()
	if err != nil {
		return 0, false, fmt.Errorf("failed to decode recorded depth: %w", err)
	}
	return depth, true, nil
}

// depthLedger is the gateway-local half: a bounded, TTL'd map of recorded
// depths. Safe for concurrent use.
type depthLedger struct {
	mu      sync.Mutex
	entries map[string]depthEntry
	maxSize int
	ttl     time.Duration
	now     func() time.Time // injectable clock for tests
}

type depthEntry struct {
	depth  int
	expiry time.Time
}

func newDepthLedger() *depthLedger {
	return &depthLedger{
		entries: make(map[string]depthEntry),
		maxSize: localDepthMaxEntries,
		ttl:     publishDepthTTL,
		now:     time.Now,
	}
}

// record keeps the higher of the existing and the new depth for key.
func (l *depthLedger) record(key string, depth int) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if cur, ok := l.entries[key]; ok && now.Before(cur.expiry) && cur.depth >= depth {
		return nil
	}
	if _, ok := l.entries[key]; !ok && len(l.entries) >= l.maxSize {
		for k, e := range l.entries {
			if !now.Before(e.expiry) {
				delete(l.entries, k)
			}
		}
		if len(l.entries) >= l.maxSize {
			return ErrPublishDepthFull
		}
	}
	l.entries[key] = depthEntry{depth: depth, expiry: now.Add(l.ttl)}
	return nil
}

// lookup returns the unexpired recorded depth for key.
func (l *depthLedger) lookup(key string) (int, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.entries[key]
	if !ok || !l.now().Before(e.expiry) {
		return 0, false
	}
	return e.depth, true
}

// RecordPublishDepth notes that the message (namespace, topic, data) is being
// published by a function invocation running at trigger depth `depth`, so the
// dispatch of that message — on this gateway or another — runs at no less than
// that depth. Call it before the publish. A depth below 1 records nothing: a
// message published outside a triggered chain is dispatched at depth 0.
//
// It returns an error when the record cannot be kept; the caller must not
// publish without it, or the chain would restart at depth 0.
func (d *PubSubDispatcher) RecordPublishDepth(ctx context.Context, namespace, topic string, data []byte, depth int) error {
	if depth < 1 {
		return nil
	}
	key := dispatchDedupKey(namespace, topic, data)
	if err := d.depthLedger.record(key, depth); err != nil {
		return fmt.Errorf("failed to record trigger depth %d for %s on %s: %w", depth, namespace, topic, err)
	}
	if d.depthStore == nil {
		return nil
	}
	// Only ever raise the shared record: a lower depth must not overwrite a
	// higher one another function recorded for the same bytes.
	if cur, ok, err := d.depthStore.get(ctx, key); err != nil {
		return fmt.Errorf("failed to read shared trigger depth for %s on %s: %w", namespace, topic, err)
	} else if ok && cur >= depth {
		return nil
	}
	if err := d.depthStore.put(ctx, key, depth); err != nil {
		return fmt.Errorf("failed to record trigger depth %d for %s on %s in the shared store: %w", depth, namespace, topic, err)
	}
	return nil
}

// publishedDepth is the depth a message arriving from libp2p is dispatched at:
// the higher of what this gateway recorded and what the shared store holds, 0
// when neither knows the message. A shared-store read that fails is logged and
// leaves the local record to decide; it is not treated as "depth 0 recorded".
func (d *PubSubDispatcher) publishedDepth(ctx context.Context, namespace, topic string, data []byte) int {
	key := dispatchDedupKey(namespace, topic, data)
	depth, _ := d.depthLedger.lookup(key)
	if d.depthStore == nil {
		return depth
	}
	shared, ok, err := d.depthStore.get(ctx, key)
	if err != nil {
		d.logger.Error("Could not read the recorded trigger depth of a published message; "+
			"dispatching at the depth this gateway knows",
			zap.String("namespace", namespace),
			zap.String("topic", topic),
			zap.Int("local_depth", depth),
			zap.Error(err),
		)
		return depth
	}
	if ok && shared > depth {
		depth = shared
	}
	return depth
}
