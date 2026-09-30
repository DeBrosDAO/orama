package pubsub

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
)

// collector is a MessageHandler that keeps what it is given.
type collector struct {
	mu   sync.Mutex
	msgs []string
}

func (c *collector) handle(_ string, data []byte) error {
	c.mu.Lock()
	c.msgs = append(c.msgs, string(data))
	c.mu.Unlock()
	return nil
}

func (c *collector) count(msg string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, m := range c.msgs {
		if m == msg {
			n++
		}
	}
	return n
}

// publishUntil publishes msg until every collector has it, or fails at the
// deadline. Gossipsub needs a moment before a fresh subscription is fed.
func publishUntil(t *testing.T, c *HTTPClient, topic, msg string, want ...*collector) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := c.Publish(context.Background(), topic, []byte(msg)); err != nil {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
		all := true
		for _, w := range want {
			all = all && w.count(msg) > 0
		}
		if all {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%q never reached every subscriber", msg)
		}
	}
}

// The bug: a second Subscribe on the same namespace and topic cancelled the
// first one's stream, so only the newest handler was ever fed.
func TestHTTPClient_Subscribe_twoHandlersOnOneTopicBothReceive(t *testing.T) {
	sock, _ := startAPI(t)
	c := NewHTTPClient(sock, "ns", zap.NewNop())
	defer c.Close()
	ctx := context.Background()

	first, second := &collector{}, &collector{}
	if err := c.Subscribe(ctx, "shared", first.handle); err != nil {
		t.Fatal(err)
	}
	if err := c.Subscribe(ctx, "shared", second.handle); err != nil {
		t.Fatal(err)
	}
	publishUntil(t, c, "shared", "m1", first, second)
}

func TestHTTPClient_SubscribeHandle_leavingKeepsTheOthers(t *testing.T) {
	sock, _ := startAPI(t)
	c := NewHTTPClient(sock, "ns", zap.NewNop())
	defer c.Close()
	ctx := context.Background()

	stays, leaves := &collector{}, &collector{}
	if _, err := c.SubscribeHandle(ctx, "shared", stays.handle); err != nil {
		t.Fatal(err)
	}
	stop, err := c.SubscribeHandle(ctx, "shared", leaves.handle)
	if err != nil {
		t.Fatal(err)
	}
	publishUntil(t, c, "shared", "before", stays, leaves)

	if err := stop(); err != nil {
		t.Fatal(err)
	}
	if err := stop(); err != nil {
		t.Fatalf("a second stop must be a no-op: %v", err)
	}
	publishUntil(t, c, "shared", "after", stays)
	if leaves.count("after") != 0 {
		t.Fatal("a handler that left still received a message")
	}
}

func TestHTTPClient_SubscribeHandle_lastToLeaveClosesTheStream(t *testing.T) {
	sock, mgr := startAPI(t)
	c := NewHTTPClient(sock, "ns", zap.NewNop())
	defer c.Close()
	ctx := context.Background()

	stopA, err := c.SubscribeHandle(ctx, "t", (&collector{}).handle)
	if err != nil {
		t.Fatal(err)
	}
	stopB, err := c.SubscribeHandle(ctx, "t", (&collector{}).handle)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := mgr.ListTopics(WithNamespace(ctx, "ns")); len(got) != 1 {
		t.Fatalf("topics with two handlers = %v, want one", got)
	}
	_ = stopA()
	if got, _ := mgr.ListTopics(WithNamespace(ctx, "ns")); len(got) != 1 {
		t.Fatalf("the stream closed while a handler was left: %v", got)
	}
	_ = stopB()
	waitTopics(t, mgr, "ns", 0)

	// A new subscriber after the stream closed opens a fresh one.
	again := &collector{}
	if _, err := c.SubscribeHandle(ctx, "t", again.handle); err != nil {
		t.Fatal(err)
	}
	publishUntil(t, c, "t", "reopened", again)
}

func TestHTTPClient_Subscribe_isEstablishedWhenItReturns(t *testing.T) {
	sock, mgr := startAPI(t)
	c := NewHTTPClient(sock, "ns", zap.NewNop())
	defer c.Close()
	if err := c.Subscribe(context.Background(), "now", (&collector{}).handle); err != nil {
		t.Fatal(err)
	}
	if got, _ := mgr.ListTopics(WithNamespace(context.Background(), "ns")); len(got) != 1 {
		t.Fatalf("the service has no subscription when Subscribe returned: %v", got)
	}
}

func TestHTTPClient_Subscribe_failureIsReturnedNotSwallowed(t *testing.T) {
	c := NewHTTPClient(shortSocketPath(t), "ns", zap.NewNop()) // nothing listens
	defer c.Close()
	if err := c.Subscribe(context.Background(), "t", (&collector{}).handle); err == nil {
		t.Fatal("subscribing to an unreachable service must fail")
	}
	// The failed attempt leaves no half-open stream behind: a later one is fresh.
	c.mu.Lock()
	n := len(c.streams)
	c.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d streams left after a failed subscribe", n)
	}
}

// The bug: open ran before the wait on the caller's context, so a first
// subscriber whose service never answered was held for the whole request
// timeout however soon its own context ended.
func TestHTTPClient_Subscribe_firstSubscriberHonoursItsContext(t *testing.T) {
	sock := shortSocketPath(t)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() { // accepts and never answers
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { _ = conn.Close() })
		}
	}()
	c := NewHTTPClient(sock, "ns", zap.NewNop())
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	err = c.Subscribe(ctx, "t", (&collector{}).handle)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Subscribe returned %v, want the context's deadline error", err)
	}
	if took := time.Since(start); took > requestTimeout/4 {
		t.Fatalf("Subscribe held its caller for %s past a 100ms context", took)
	}
	// The abandoned attempt leaves no stream behind once the opener unwinds.
	deadline := time.Now().Add(2 * time.Second)
	for {
		c.mu.Lock()
		n := len(c.streams)
		c.mu.Unlock()
		if n == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d streams left after the only subscriber gave up", n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A publisher's sequential publishes, each answered before the next is sent,
// reach a subscriber in the order they were sent.
func TestHTTPClient_Publish_sequentialPublishesArriveInOrder(t *testing.T) {
	sock, _ := startAPI(t)
	c := NewHTTPClient(sock, "ns", zap.NewNop())
	defer c.Close()

	sub := &collector{}
	if err := c.Subscribe(context.Background(), "ordered", sub.handle); err != nil {
		t.Fatal(err)
	}
	publishUntil(t, c, "ordered", "ready", sub) // the subscription is being fed

	const n = 200
	want := make([]string, n)
	for i := range want {
		want[i] = fmt.Sprintf("m%03d", i)
		if err := c.Publish(context.Background(), "ordered", []byte(want[i])); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for sub.count(want[n-1]) == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("%q never arrived", want[n-1])
		}
		time.Sleep(10 * time.Millisecond)
	}
	sub.mu.Lock()
	defer sub.mu.Unlock()
	var got []string
	for _, m := range sub.msgs {
		if m != "ready" {
			got = append(got, m)
		}
	}
	if len(got) != n {
		t.Fatalf("subscriber got %d of %d messages", len(got), n)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("message %d arrived as %q, want %q", i, got[i], want[i])
		}
	}
}

func TestHTTPClient_Unsubscribe_pairsWithSubscribeAndClosesAtZero(t *testing.T) {
	sock, mgr := startAPI(t)
	c := NewHTTPClient(sock, "ns", zap.NewNop())
	defer c.Close()
	ctx := WithNamespace(context.Background(), "tenant") // the override, not the default

	if err := c.Subscribe(ctx, "t", (&collector{}).handle); err != nil {
		t.Fatal(err)
	}
	if err := c.Subscribe(ctx, "t", (&collector{}).handle); err != nil {
		t.Fatal(err)
	}
	if err := c.Unsubscribe(ctx, "t"); err != nil {
		t.Fatal(err)
	}
	if got, _ := mgr.ListTopics(ctx); len(got) != 1 {
		t.Fatalf("one subscriber is left but topics = %v", got)
	}
	if err := c.Unsubscribe(ctx, "t"); err != nil {
		t.Fatal(err)
	}
	waitTopics(t, mgr, "tenant", 0)
	if err := c.Unsubscribe(ctx, "t"); err != nil {
		t.Fatalf("unsubscribing with nothing subscribed must be a no-op: %v", err)
	}
}

func TestHTTPClient_Subscribe_concurrentSubscribersShareOneStream(t *testing.T) {
	sock, _ := startAPI(t)
	c := NewHTTPClient(sock, "ns", zap.NewNop())
	defer c.Close()
	const n = 8
	cols := make([]*collector, n)
	var wg sync.WaitGroup
	for i := range cols {
		cols[i] = &collector{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.SubscribeHandle(context.Background(), "burst", cols[i].handle); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	c.mu.Lock()
	streams := len(c.streams)
	c.mu.Unlock()
	if streams != 1 {
		t.Fatalf("%d streams for one topic, want 1", streams)
	}
	publishUntil(t, c, "burst", "hi", cols...)
}

func TestHTTPClient_Close_endsEveryStream(t *testing.T) {
	sock, mgr := startAPI(t)
	c := NewHTTPClient(sock, "ns", zap.NewNop())
	if err := c.Subscribe(context.Background(), "t", (&collector{}).handle); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	waitTopics(t, mgr, "ns", 0)
}

// waitTopics polls until the service lists want topics for ns; the service
// notices a closed stream asynchronously.
func waitTopics(t *testing.T, mgr *Manager, ns string, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		got, _ := mgr.ListTopics(WithNamespace(context.Background(), ns))
		if len(got) == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("service lists %v for %q, want %d topics", got, ns, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
