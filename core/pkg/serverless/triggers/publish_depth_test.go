package triggers

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
)

// A publish a triggered function makes arrives back at the dispatcher over
// libp2p with only its payload. These tests drive the same two calls the
// runtime makes: the host's RecordPublishDepth before it publishes, and the
// Subscribe handler's dispatch(..., fromWire=true) when the message returns.
// The store is nil-db on purpose: reaching getMatches panics, so "the dispatch
// got past the depth gate" is observable as a recovered panic.

func newDepthDispatcher() *PubSubDispatcher {
	return NewPubSubDispatcher(NewPubSubTriggerStore(nil, zap.NewNop()), nil, nil, nil, zap.NewNop())
}

// reachesTriggerLookup reports whether a dispatch got past every gate to the
// trigger lookup.
func reachesTriggerLookup(d *PubSubDispatcher, ns, topic string, data []byte, fromWire bool) bool {
	reached := false
	func() {
		defer func() {
			if recover() != nil {
				reached = true
			}
		}()
		d.dispatch(context.Background(), ns, topic, data, 0, fromWire)
	}()
	return reached
}

// The bug: a function that republishes, once per trigger, ran forever because
// each hop came back at depth 0. The chain must stop at maxTriggerDepth.
func TestDispatch_aChainOfTriggeredRepublishesStopsAtTheLimit(t *testing.T) {
	d := newDepthDispatcher()
	ctx := context.Background()

	// A client publish starts the chain: dispatched at 0, its handler runs at 1.
	if !reachesTriggerLookup(d, "ns", "loop", []byte("seed"), true) {
		t.Fatal("the client's first publish must dispatch")
	}

	handlerDepth, dispatched := 1, 0
	for hop := 0; hop < 50; hop++ {
		payload := []byte(fmt.Sprintf("hop-%d", hop))
		// The handler republishes: the host records its depth, then the message
		// returns over libp2p.
		if err := d.RecordPublishDepth(ctx, "ns", "loop", payload, handlerDepth); err != nil {
			t.Fatalf("RecordPublishDepth: %v", err)
		}
		if !reachesTriggerLookup(d, "ns", "loop", payload, true) {
			break
		}
		dispatched++
		handlerDepth++
	}
	if dispatched != maxTriggerDepth-1 {
		t.Fatalf("the chain dispatched %d more times; want it to stop after %d (depth %d)",
			dispatched, maxTriggerDepth-1, maxTriggerDepth)
	}
}

// Without a record the same republishes never stop: this is the behaviour the
// fix removes, kept as the control that the test above depends on the record.
func TestDispatch_withoutARecordADeepRepublishRestartsAtZero(t *testing.T) {
	d := newDepthDispatcher()
	for hop := 0; hop < 2*maxTriggerDepth; hop++ {
		if !reachesTriggerLookup(d, "ns", "loop", []byte(fmt.Sprintf("hop-%d", hop)), true) {
			t.Fatalf("hop %d was stopped with nothing recorded", hop)
		}
	}
}

func TestDispatch_aClientPublishStartsAtDepthZero(t *testing.T) {
	d := newDepthDispatcher()
	ctx := context.Background()

	// A function recorded a deep publish of OTHER bytes; a client's message is
	// unaffected by it.
	if err := d.RecordPublishDepth(ctx, "ns", "t", []byte("function's message"), maxTriggerDepth); err != nil {
		t.Fatalf("RecordPublishDepth: %v", err)
	}
	client := []byte("client's message")
	if got := d.publishedDepth(ctx, "ns", "t", client); got != 0 {
		t.Errorf("an unrecorded message is at depth %d; want 0", got)
	}
	if !reachesTriggerLookup(d, "ns", "t", client, true) {
		t.Error("a client's message must dispatch")
	}
}

// The HTTP publish hook is a client's publish: it is dispatched at the depth it
// is given, whatever the ledger holds for the same bytes.
func TestDispatch_theHTTPHookKeepsTheDepthItIsGiven(t *testing.T) {
	d := newDepthDispatcher()
	data := []byte("bytes a function also published")
	if err := d.RecordPublishDepth(context.Background(), "ns", "t", data, maxTriggerDepth); err != nil {
		t.Fatalf("RecordPublishDepth: %v", err)
	}
	if !reachesTriggerLookup(d, "ns", "t", data, false) {
		t.Error("an HTTP-hook dispatch at depth 0 must not be raised by the ledger")
	}
}

// A record raises a message's depth and nothing lowers it: a second record with
// a smaller depth for the same bytes, here and in the shared store, is ignored.
func TestRecordPublishDepth_aLowerDepthNeverReplacesAHigherOne(t *testing.T) {
	d := newDepthDispatcher()
	shared := newFakeDepthStore()
	d.depthStore = shared
	ctx := context.Background()
	data := []byte("same bytes")

	if err := d.RecordPublishDepth(ctx, "ns", "t", data, 4); err != nil {
		t.Fatalf("RecordPublishDepth(4): %v", err)
	}
	if err := d.RecordPublishDepth(ctx, "ns", "t", data, 1); err != nil {
		t.Fatalf("RecordPublishDepth(1): %v", err)
	}
	if got := d.publishedDepth(ctx, "ns", "t", data); got != 4 {
		t.Errorf("depth after a lower record = %d; want 4", got)
	}
	if got, _, _ := shared.get(ctx, dispatchDedupKey("ns", "t", data)); got != 4 {
		t.Errorf("shared depth after a lower record = %d; want 4", got)
	}
}

func TestRecordPublishDepth_belowOneRecordsNothing(t *testing.T) {
	d := newDepthDispatcher()
	shared := newFakeDepthStore()
	d.depthStore = shared
	for _, depth := range []int{0, -3} {
		if err := d.RecordPublishDepth(context.Background(), "ns", "t", []byte("x"), depth); err != nil {
			t.Fatalf("RecordPublishDepth(%d): %v", depth, err)
		}
	}
	if len(d.depthLedger.entries) != 0 || len(shared.m) != 0 {
		t.Error("a publish outside a triggered chain left a depth record")
	}
}

// The gateway that wins the dispatch claim may not be the one that published.
func TestPublishedDepth_anotherGatewayReadsTheSharedRecord(t *testing.T) {
	shared := newFakeDepthStore()
	publisher, claimer := newDepthDispatcher(), newDepthDispatcher()
	publisher.depthStore, claimer.depthStore = shared, shared
	data := []byte("crosses nodes")

	if err := publisher.RecordPublishDepth(context.Background(), "ns", "t", data, maxTriggerDepth); err != nil {
		t.Fatalf("RecordPublishDepth: %v", err)
	}
	if reachesTriggerLookup(claimer, "ns", "t", data, true) {
		t.Error("the other gateway dispatched a message already at the depth limit")
	}
}

func TestRecordPublishDepth_aSharedStoreFailureRefusesTheRecord(t *testing.T) {
	d := newDepthDispatcher()
	d.depthStore = &fakeDepthStore{m: map[string]int{}, putErr: errors.New("olric down")}
	err := d.RecordPublishDepth(context.Background(), "ns", "t", []byte("x"), 2)
	if err == nil {
		t.Fatal("a depth that could not be kept must be an error, or the publish would restart the chain")
	}
}

func TestPublishedDepth_aSharedStoreReadFailureFallsBackToTheLocalRecord(t *testing.T) {
	d := newDepthDispatcher()
	data := []byte("x")
	if err := d.RecordPublishDepth(context.Background(), "ns", "t", data, 3); err != nil {
		t.Fatalf("RecordPublishDepth: %v", err)
	}
	d.depthStore = &fakeDepthStore{m: map[string]int{}, getErr: errors.New("olric down")}
	if got := d.publishedDepth(context.Background(), "ns", "t", data); got != 3 {
		t.Errorf("depth = %d; want the local record 3", got)
	}
}

func TestDepthLedger_refusesPastTheCapAndExpires(t *testing.T) {
	l := newDepthLedger()
	l.maxSize = 2
	now := time.Now()
	l.now = func() time.Time { return now }

	if err := l.record("a", 1); err != nil {
		t.Fatal(err)
	}
	if err := l.record("b", 1); err != nil {
		t.Fatal(err)
	}
	if err := l.record("c", 1); !errors.Is(err, ErrPublishDepthFull) {
		t.Errorf("record past the cap = %v; want ErrPublishDepthFull", err)
	}
	if err := l.record("a", 2); err != nil {
		t.Errorf("raising an existing record must not need room: %v", err)
	}

	now = now.Add(publishDepthTTL + time.Second)
	if _, ok := l.lookup("a"); ok {
		t.Error("an expired record was returned")
	}
	if err := l.record("c", 1); err != nil {
		t.Errorf("expired records must free room: %v", err)
	}
}

type fakeDepthStore struct {
	mu     sync.Mutex
	m      map[string]int
	putErr error
	getErr error
}

func newFakeDepthStore() *fakeDepthStore { return &fakeDepthStore{m: map[string]int{}} }

func (f *fakeDepthStore) put(_ context.Context, key string, depth int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.putErr != nil {
		return f.putErr
	}
	f.m[key] = depth
	return nil
}

func (f *fakeDepthStore) get(_ context.Context, key string) (int, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return 0, false, f.getErr
	}
	v, ok := f.m[key]
	return v, ok, nil
}

// A function that republishes the same bytes every time (a retry, a heartbeat)
// sends one message per hop. The once-per-publish claims were keyed on the bytes
// alone, so the second hop was swallowed as a duplicate of the first and the
// chain stalled at depth 2 instead of running to the limit.
func TestDispatch_constantPayloadRepublishChainReachesTheLimit(t *testing.T) {
	d := newDepthDispatcher()
	ctx := context.Background()
	payload := []byte(`{"again":true}`)

	if !reachesTriggerLookup(d, "ns", "loop", []byte(`{"start":true}`), true) {
		t.Fatal("the client's first publish must dispatch")
	}
	dispatched := 0
	for handlerDepth := 1; handlerDepth <= maxTriggerDepth+2; handlerDepth++ {
		if err := d.RecordPublishDepth(ctx, "ns", "loop", payload, handlerDepth); err != nil {
			t.Fatalf("RecordPublishDepth: %v", err)
		}
		if !reachesTriggerLookup(d, "ns", "loop", payload, true) {
			break
		}
		dispatched++
	}
	if want := maxTriggerDepth - 1; dispatched != want {
		t.Fatalf("a constant-payload chain dispatched %d hops, want %d (one per depth below the limit)", dispatched, want)
	}
}

// The same message delivered twice at one depth is still a duplicate.
func TestDispatch_sameMessageSameDepthStaysDeduped(t *testing.T) {
	d := newDepthDispatcher()
	payload := []byte(`{"again":true}`)
	if err := d.RecordPublishDepth(context.Background(), "ns", "loop", payload, 2); err != nil {
		t.Fatalf("RecordPublishDepth: %v", err)
	}
	if !reachesTriggerLookup(d, "ns", "loop", payload, true) {
		t.Fatal("the first delivery must dispatch")
	}
	if reachesTriggerLookup(d, "ns", "loop", payload, true) {
		t.Fatal("a second delivery of the same message at the same depth must be deduped")
	}
}

type countingDepthStore struct {
	*fakeDepthStore
	mu   sync.Mutex
	gets int
}

func (c *countingDepthStore) get(ctx context.Context, key string) (int, bool, error) {
	c.mu.Lock()
	c.gets++
	c.mu.Unlock()
	return c.fakeDepthStore.get(ctx, key)
}

// Repeat deliveries of a message nothing recorded must not each read Olric.
func TestPublishedDepth_repeatDeliveriesOfAnUnrecordedMessageReadOnce(t *testing.T) {
	d := newDepthDispatcher()
	store := &countingDepthStore{fakeDepthStore: newFakeDepthStore()}
	d.depthStore = store
	for i := 0; i < 5; i++ {
		if got := d.publishedDepth(context.Background(), "ns", "t", []byte("x")); got != 0 {
			t.Fatalf("depth = %d; want 0", got)
		}
	}
	if store.gets != 1 {
		t.Errorf("shared reads = %d; want 1", store.gets)
	}
}

func TestPublishedDepth_missExpiresAndRecordClearsIt(t *testing.T) {
	d := newDepthDispatcher()
	now := time.Now()
	d.depthMisses.now = func() time.Time { return now }
	store := &countingDepthStore{fakeDepthStore: newFakeDepthStore()}
	d.depthStore = store
	data := []byte("x")

	d.publishedDepth(context.Background(), "ns", "t", data)
	now = now.Add(depthMissTTL + time.Millisecond)
	d.publishedDepth(context.Background(), "ns", "t", data)
	if store.gets != 2 {
		t.Errorf("reads after the miss expired = %d; want 2", store.gets)
	}

	if err := d.RecordPublishDepth(context.Background(), "ns", "t", data, 4); err != nil {
		t.Fatal(err)
	}
	if got := d.publishedDepth(context.Background(), "ns", "t", data); got != 4 {
		t.Errorf("depth after a record = %d; want 4", got)
	}
}

func TestPublishedDepth_readFailureIsNotCachedAsAMiss(t *testing.T) {
	d := newDepthDispatcher()
	store := &countingDepthStore{fakeDepthStore: &fakeDepthStore{m: map[string]int{}, getErr: errors.New("olric down")}}
	d.depthStore = store
	for i := 0; i < 3; i++ {
		d.publishedDepth(context.Background(), "ns", "t", []byte("x"))
	}
	if store.gets != 3 {
		t.Errorf("reads = %d; a failed read must be retried, not cached", store.gets)
	}
}
