package chainread

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const testHeadInterval = 10 * time.Millisecond

// fakeNode serves CometBFT's /status with a head the test moves.
type fakeNode struct {
	mu     sync.Mutex
	height int64
	chain  string
	down   bool
	hits   atomic.Int64
}

func (n *fakeNode) set(height int64, chain string, down bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.height, n.chain, n.down = height, chain, down
}

func (n *fakeNode) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	n.hits.Add(1)
	n.mu.Lock()
	height, chain, down := n.height, n.chain, n.down
	n.mu.Unlock()
	if down {
		http.Error(w, "boom", http.StatusInternalServerError)
		return
	}
	_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":-1,"result":{"node_info":{"network":%q},"sync_info":{"latest_block_height":"%d","latest_block_time":"2026-10-11T00:00:00Z","catching_up":false}}}`, chain, height)
}

// headGateway is a proxy over a fake node, served on a test server, with a fast reader.
func headGateway(t *testing.T) (*Proxy, *fakeNode, *httptest.Server) {
	t.Helper()
	node := &fakeNode{height: 100, chain: "net-1"}
	rpc := httptest.NewServer(node)
	t.Cleanup(rpc.Close)
	p, err := New(Config{RPCURL: rpc.URL, RESTURL: "http://127.0.0.1:9", IndexURL: "http://127.0.0.1:9"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	p.heads = newHeadHub(p.readHead, testHeadInterval)
	gw := httptest.NewServer(p)
	t.Cleanup(gw.Close)
	return p, node, gw
}

// sseClient reads the events of one head stream.
type sseClient struct {
	events chan [2]string
	cancel context.CancelFunc
	body   *bufio.Scanner
	resp   *http.Response
}

func openStream(t *testing.T, gw *httptest.Server) *sseClient {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, gw.URL+"/v1/chain/head/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatalf("open the stream: %v", err)
	}
	c := &sseClient{events: make(chan [2]string, 64), cancel: cancel, resp: resp}
	t.Cleanup(c.close)
	go c.read()
	return c
}

func (c *sseClient) close() {
	c.cancel()
	_ = c.resp.Body.Close()
}

// read parses the stream into (event name, data) pairs; comments and retry lines are skipped.
func (c *sseClient) read() {
	defer close(c.events)
	sc := bufio.NewScanner(c.resp.Body)
	var name, data string
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if name != "" {
				c.events <- [2]string{name, data}
			}
			name, data = "", ""
		case strings.HasPrefix(line, "event: "):
			name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			data = strings.TrimPrefix(line, "data: ")
		}
	}
}

func (c *sseClient) next(t *testing.T) [2]string {
	t.Helper()
	select {
	case ev, ok := <-c.events:
		if !ok {
			t.Fatal("the stream ended")
		}
		return ev
	case <-time.After(5 * time.Second):
		t.Fatal("no event within 5 s")
		return [2]string{}
	}
}

func (c *sseClient) quiet(t *testing.T, d time.Duration) {
	t.Helper()
	select {
	case ev := <-c.events:
		t.Fatalf("unexpected event %v", ev)
	case <-time.After(d):
	}
}

func TestHeadStream_sendsTheCurrentHeadThenEachNewHeight(t *testing.T) {
	_, node, gw := headGateway(t)
	c := openStream(t, gw)

	if got := c.resp.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("Content-Type %q", got)
	}
	if got := c.resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control %q", got)
	}
	first := c.next(t)
	if first[0] != "head" || first[1] != `{"height":100,"time":"2026-10-11T00:00:00Z","chain_id":"net-1"}` {
		t.Fatalf("first event %v", first)
	}
	c.quiet(t, 15*testHeadInterval) // the same height is not announced again

	node.set(101, "net-1", false)
	if got := c.next(t); got[0] != "head" || !strings.Contains(got[1], `"height":101`) {
		t.Fatalf("next event %v", got)
	}
}

func TestHeadStream_oneReaderServesEveryStream(t *testing.T) {
	_, node, gw := headGateway(t)
	var streams []*sseClient
	for i := 0; i < 12; i++ {
		streams = append(streams, openStream(t, gw))
	}
	for _, c := range streams {
		if got := c.next(t); got[0] != "head" {
			t.Fatalf("event %v", got)
		}
	}
	before := node.hits.Load()
	time.Sleep(20 * testHeadInterval)
	reads := node.hits.Load() - before
	if reads > 30 {
		t.Errorf("%d status reads in 20 intervals for 12 streams: the node is asked once per stream, not once per interval", reads)
	}
	node.set(105, "net-1", false)
	for i, c := range streams {
		if got := c.next(t); !strings.Contains(got[1], `"height":105`) {
			t.Errorf("stream %d got %v", i, got)
		}
	}
}

func TestHeadStream_theReaderStopsWhenTheLastStreamLeaves(t *testing.T) {
	_, node, gw := headGateway(t)
	c := openStream(t, gw)
	c.next(t)
	c.close()
	time.Sleep(10 * testHeadInterval)
	settled := node.hits.Load()
	time.Sleep(10 * testHeadInterval)
	if after := node.hits.Load(); after > settled+1 {
		t.Errorf("the node was still read %d times after the last stream went", after-settled)
	}
}

func TestHeadStream_aNodeThatCannotBeReadIsToldOnceAndThenRecovers(t *testing.T) {
	_, node, gw := headGateway(t)
	c := openStream(t, gw)
	c.next(t)

	node.set(0, "", true)
	if got := c.next(t); got[0] != "unavailable" || !strings.Contains(got[1], "could not be read") {
		t.Fatalf("event %v", got)
	}
	c.quiet(t, 15*testHeadInterval) // told once, not on every failed read

	node.set(100, "net-1", false)
	if got := c.next(t); got[0] != "head" || !strings.Contains(got[1], `"height":100`) {
		t.Fatalf("after the node is back: %v", got)
	}

	late := openStream(t, gw)
	late.next(t)
	node.set(0, "", true)
	if got := late.next(t); got[0] != "unavailable" {
		t.Fatalf("a stream that opens on a failing node: %v", got)
	}
}

func TestHeadStream_aNetworkThatStartedAgainIsAnnouncedAtALowerHeight(t *testing.T) {
	_, node, gw := headGateway(t)
	c := openStream(t, gw)
	c.next(t)

	node.set(3, "net-2", false)
	got := c.next(t)
	if got[0] != "head" || !strings.Contains(got[1], `"chain_id":"net-2"`) || !strings.Contains(got[1], `"height":3`) {
		t.Fatalf("event %v", got)
	}
}

func TestHeadStream_refusesWhatItDoesNotServe(t *testing.T) {
	p, _, _ := headGateway(t)
	for _, tc := range []struct {
		method, target string
		code           int
	}{
		{http.MethodPost, "/v1/chain/head/stream", http.StatusMethodNotAllowed},
		{http.MethodGet, "/v1/chain/head/stream?from=1", http.StatusBadRequest},
		{http.MethodGet, "/v1/chain/head/stream/", http.StatusNotFound},
		{http.MethodGet, "/v1/chain/head", http.StatusNotFound},
	} {
		rec := httptest.NewRecorder()
		p.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.target, nil))
		if rec.Code != tc.code {
			t.Errorf("%s %s: HTTP %d, want %d", tc.method, tc.target, rec.Code, tc.code)
		}
	}
}

func TestHeadStream_pastTheLimitIsRefusedWithARetry(t *testing.T) {
	p, _, gw := headGateway(t)
	p.headSlots = make(chan struct{}, 1)
	first := openStream(t, gw)
	first.next(t)

	resp, err := http.Get(gw.URL + "/v1/chain/head/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable || resp.Header.Get("Retry-After") == "" {
		t.Errorf("second stream: HTTP %d, Retry-After %q", resp.StatusCode, resp.Header.Get("Retry-After"))
	}

	first.close()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		again, err := http.Get(gw.URL + "/v1/chain/head/stream")
		if err != nil {
			t.Fatal(err)
		}
		code := again.StatusCode
		again.Body.Close()
		if code == http.StatusOK {
			return
		}
		time.Sleep(testHeadInterval)
	}
	t.Error("the slot was not given back when the first stream closed")
}

func TestHeadStream_endsAtItsMaximumAge(t *testing.T) {
	p, _, gw := headGateway(t)
	p.headMaxAge = 80 * time.Millisecond
	c := openStream(t, gw)
	c.next(t)
	select {
	case _, ok := <-c.events:
		if ok {
			t.Fatal("an event after the stream should have ended")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the stream did not end at its maximum age")
	}
}

func TestReadHead_refusesAStatusWithNoHead(t *testing.T) {
	for name, body := range map[string]string{
		"not json":      `<html>`,
		"no height":     `{"result":{"node_info":{"network":"n"},"sync_info":{"latest_block_height":"","latest_block_time":"t"}}}`,
		"zero height":   `{"result":{"node_info":{"network":"n"},"sync_info":{"latest_block_height":"0","latest_block_time":"t"}}}`,
		"no chain id":   `{"result":{"node_info":{},"sync_info":{"latest_block_height":"5","latest_block_time":"t"}}}`,
		"word height":   `{"result":{"node_info":{"network":"n"},"sync_info":{"latest_block_height":"five"}}}`,
		"rpc error":     `{"error":{"message":"x"}}`,
		"empty result":  `{"result":{}}`,
		"wrong typing":  `{"result":{"node_info":"n"}}`,
		"height as int": `{"result":{"node_info":{"network":"n"},"sync_info":{"latest_block_height":5}}}`,
	} {
		rpc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
		p, err := New(Config{RPCURL: rpc.URL, RESTURL: "http://127.0.0.1:9", IndexURL: "http://127.0.0.1:9"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := p.readHead(context.Background()); err == nil {
			t.Errorf("%s: a status with no head was accepted", name)
		}
		rpc.Close()
	}
}
