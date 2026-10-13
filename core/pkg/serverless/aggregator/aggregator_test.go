package aggregator

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestBuffer_panics_on_zero_window(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic when WindowMs <= 0")
		}
	}()
	a := New(zap.NewNop(), time.Second)
	a.Buffer(BufferRequest{
		Namespace:  "ns",
		FunctionID: "fn",
		TriggerID:  "tr",
		WindowMs:   0,
		FlushFn:    func(ctx context.Context, payload []byte) {},
		Event:      Event{Topic: "t"},
	})
}

func TestBuffer_flushes_on_timer(t *testing.T) {
	a := New(zap.NewNop(), 5*time.Second)

	var (
		got   []Event
		gotMu sync.Mutex
		done  = make(chan struct{})
	)

	flush := func(ctx context.Context, payload []byte) {
		var p BatchedPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			t.Errorf("unmarshal: %v", err)
		}
		gotMu.Lock()
		got = append(got, p.Events...)
		gotMu.Unlock()
		close(done)
	}

	for i := 0; i < 3; i++ {
		a.Buffer(BufferRequest{
			Namespace:  "ns",
			FunctionID: "fn",
			TriggerID:  "tr",
			WindowMs:   100, // short window so test runs fast
			FlushFn:    flush,
			Event:      Event{Topic: "presence:user", Data: json.RawMessage(`"e"`)},
		})
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("flush did not fire within 2s")
	}

	gotMu.Lock()
	defer gotMu.Unlock()
	if len(got) != 3 {
		t.Errorf("expected 3 buffered events, got %d", len(got))
	}
}

func TestBuffer_flushes_on_max_batch_size(t *testing.T) {
	a := New(zap.NewNop(), 5*time.Second)

	var (
		flushCount int32
		flushSize  int32
		done       = make(chan struct{})
	)

	flush := func(ctx context.Context, payload []byte) {
		var p BatchedPayload
		_ = json.Unmarshal(payload, &p)
		atomic.AddInt32(&flushCount, 1)
		atomic.StoreInt32(&flushSize, int32(len(p.Events)))
		select {
		case <-done:
		default:
			close(done)
		}
	}

	for i := 0; i < 5; i++ {
		a.Buffer(BufferRequest{
			Namespace:    "ns",
			FunctionID:   "fn",
			TriggerID:    "tr",
			WindowMs:     30_000, // long enough that the timer won't fire
			MaxBatchSize: 5,
			FlushFn:      flush,
			Event:        Event{Topic: "t"},
		})
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("max-batch flush did not fire")
	}

	if atomic.LoadInt32(&flushCount) != 1 {
		t.Errorf("expected 1 flush, got %d", flushCount)
	}
	if atomic.LoadInt32(&flushSize) != 5 {
		t.Errorf("expected batch size 5, got %d", flushSize)
	}
}

func TestBuffer_separate_keys_independent(t *testing.T) {
	a := New(zap.NewNop(), 5*time.Second)

	var (
		mu     sync.Mutex
		counts = map[string]int{}
		flush  = func(name string) FlushFn {
			return func(ctx context.Context, payload []byte) {
				var p BatchedPayload
				_ = json.Unmarshal(payload, &p)
				mu.Lock()
				counts[name] += len(p.Events)
				mu.Unlock()
			}
		}
	)

	a.Buffer(BufferRequest{
		Namespace: "ns", FunctionID: "fn", TriggerID: "tr-A",
		WindowMs: 100, FlushFn: flush("A"),
		Event: Event{Topic: "a"},
	})
	a.Buffer(BufferRequest{
		Namespace: "ns", FunctionID: "fn", TriggerID: "tr-B",
		WindowMs: 100, FlushFn: flush("B"),
		Event: Event{Topic: "b"},
	})
	a.Buffer(BufferRequest{
		Namespace: "ns", FunctionID: "fn", TriggerID: "tr-A",
		WindowMs: 100, FlushFn: flush("A"),
		Event: Event{Topic: "a2"},
	})

	time.Sleep(500 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if counts["A"] != 2 {
		t.Errorf("A: expected 2 events, got %d", counts["A"])
	}
	if counts["B"] != 1 {
		t.Errorf("B: expected 1 event, got %d", counts["B"])
	}
}

func TestShutdown_flushes_all_buffers(t *testing.T) {
	a := New(zap.NewNop(), 2*time.Second)

	var flushed int32
	flush := func(ctx context.Context, payload []byte) {
		atomic.AddInt32(&flushed, 1)
	}

	for i := 0; i < 4; i++ {
		a.Buffer(BufferRequest{
			Namespace: "ns", FunctionID: "fn", TriggerID: "tr",
			WindowMs: 30_000,
			FlushFn:  flush,
			Event:    Event{Topic: "t"},
		})
	}
	// Different trigger key — should produce a separate flush.
	a.Buffer(BufferRequest{
		Namespace: "ns", FunctionID: "fn", TriggerID: "other",
		WindowMs: 30_000,
		FlushFn:  flush,
		Event:    Event{Topic: "t2"},
	})

	a.Shutdown(2 * time.Second)

	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt32(&flushed) < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("expected 2 flushes from Shutdown, got %d", flushed)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestShutdown_skips_empty_buffers(t *testing.T) {
	a := New(zap.NewNop(), 2*time.Second)

	var flushed int32
	flush := func(ctx context.Context, payload []byte) {
		atomic.AddInt32(&flushed, 1)
	}

	// Add an event to create the buffer entry, then drain via size flush.
	a.Buffer(BufferRequest{
		Namespace: "ns", FunctionID: "fn", TriggerID: "tr",
		WindowMs: 30_000, MaxBatchSize: 1,
		FlushFn: flush, Event: Event{Topic: "t"},
	})

	// Wait for the size-triggered flush to drain.
	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt32(&flushed) < 1 {
		if time.Now().After(deadline) {
			t.Fatal("size flush didn't fire")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Now the buffer is empty. Shutdown should not flush again.
	a.Shutdown(2 * time.Second)
	time.Sleep(200 * time.Millisecond)
	if atomic.LoadInt32(&flushed) != 1 {
		t.Errorf("Shutdown flushed an empty buffer: total flushes %d", flushed)
	}
}

func TestStats_reports_buffered_state(t *testing.T) {
	a := New(zap.NewNop(), 2*time.Second)
	flush := func(ctx context.Context, payload []byte) {}

	a.Buffer(BufferRequest{Namespace: "ns", FunctionID: "fn", TriggerID: "a", WindowMs: 30_000, FlushFn: flush, Event: Event{Topic: "t"}})
	a.Buffer(BufferRequest{Namespace: "ns", FunctionID: "fn", TriggerID: "a", WindowMs: 30_000, FlushFn: flush, Event: Event{Topic: "t"}})
	a.Buffer(BufferRequest{Namespace: "ns", FunctionID: "fn", TriggerID: "b", WindowMs: 30_000, FlushFn: flush, Event: Event{Topic: "t"}})

	bufs, evs := a.Stats()
	if bufs != 2 {
		t.Errorf("expected 2 buffers, got %d", bufs)
	}
	if evs != 3 {
		t.Errorf("expected 3 buffered events, got %d", evs)
	}
}

func TestBuffer_concurrent_writes_no_race(t *testing.T) {
	// Run with -race: this should not detect any data races.
	a := New(zap.NewNop(), 2*time.Second)
	flush := func(ctx context.Context, payload []byte) {}

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				a.Buffer(BufferRequest{
					Namespace:  "ns",
					FunctionID: "fn",
					TriggerID:  "tr",
					WindowMs:   200,
					FlushFn:    flush,
					Event:      Event{Topic: "t"},
				})
			}
		}(g)
	}
	wg.Wait()
	// Drain.
	a.Shutdown(2 * time.Second)
}

func TestBuffer_payload_includes_batched_true_and_topic(t *testing.T) {
	a := New(zap.NewNop(), 2*time.Second)

	got := make(chan BatchedPayload, 1)
	flush := func(ctx context.Context, payload []byte) {
		var p BatchedPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			t.Errorf("unmarshal: %v", err)
		}
		got <- p
	}

	a.Buffer(BufferRequest{
		Namespace: "ns", FunctionID: "fn", TriggerID: "tr",
		WindowMs: 50, FlushFn: flush,
		Event: Event{Topic: "presence:user-1", Data: json.RawMessage(`{"x":1}`)},
	})

	select {
	case p := <-got:
		if !p.Batched {
			t.Error("payload should have Batched=true")
		}
		if len(p.Events) != 1 || p.Events[0].Topic != "presence:user-1" {
			t.Errorf("unexpected events: %+v", p.Events)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("flush did not fire")
	}
}
