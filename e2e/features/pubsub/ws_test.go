//go:build e2e_fleet

package pubsub

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/evidence"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

// Pub/sub routes (docs/whitepaper/technical-reference/appendices/i-api-surface.md "Pub/sub"); shapes are
// core/pkg/gateway/handlers/pubsub.
const (
	pathPublish  = "/v1/pubsub/publish"
	pathBatch    = "/v1/pubsub/publish-batch"
	pathTopics   = "/v1/pubsub/topics"
	pathPresence = "/v1/pubsub/presence"
	pathWS       = "/v1/pubsub/ws"
	// deliveryBudget bounds one message reaching a subscriber that is known
	// to be wired up.
	deliveryBudget = 10 * time.Second
	// meshBudget bounds a fresh subscription becoming reachable from another
	// node (GossipSub mesh formation over the overlay).
	meshBudget = time.Minute
	// frameBuffer holds frames the reader has received and the test not yet read.
	frameBuffer = 1024
	pollEvery   = time.Second
	// settleWindow is how long the tests keep listening after the last message
	// they waited for, for a duplicate or late frame: publishing is
	// synchronous, but frames of separate requests are not ordered.
	settleWindow = 5 * time.Second
	// envelopeSkew bounds the server timestamp of a frame against the runner's
	// clock.
	envelopeSkew = time.Minute
)

// frame is one message as the socket delivers it: an envelope
// {data (base64), timestamp, topic} (handlers/pubsub/ws_client.go).
type frame struct {
	Data      []byte
	Topic     string
	Timestamp int64
}

// sub is an open subscription socket with a reader goroutine.
type sub struct {
	conn   *websocket.Conn
	frames chan frame
	// closed receives the read error that ended the socket (a *websocket.CloseError on a close frame).
	closed chan error
}

// dial opens a WebSocket to pathAndQuery on c, through the node at ip when
// ip is set, with who's credential. The handshake response is returned for
// refusal tests.
func dial(t testing.TB, c *gw.Client, ip, pathAndQuery string, who tenancy.Cred) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	u := "wss://" + strings.TrimPrefix(c.BaseURL, "https://") + pathAndQuery
	h := http.Header{}
	if who.Bearer != "" {
		h.Set("Authorization", "Bearer "+who.Bearer)
	}
	if who.APIKey != "" {
		h.Set("X-API-Key", who.APIKey)
	}
	d := websocket.Dialer{TLSClientConfig: c.TLS, HandshakeTimeout: gw.WSHandshakeBudget}
	if ip != "" {
		d.NetDialContext = tenancy.PinnedDial(ip)
	}
	start := time.Now()
	conn, resp, err := d.DialContext(t.Context(), u, h)
	rec := evidence.Record{Kind: evidence.KindHTTP, Test: t.Name(), Summary: "WS " + u + " via " + ip,
		DurationMS: time.Since(start).Milliseconds()}
	if resp != nil {
		rec.Status = resp.StatusCode
	}
	if err != nil {
		rec.Error = err.Error()
	}
	if recErr := c.Recorder().Add(rec); recErr != nil {
		t.Errorf("failed to record the WebSocket dial: %v", recErr)
	}
	return conn, resp, err
}

// subscribe opens a subscription to topic (extra query parameters in q) and
// fails the test unless the upgrade succeeds. The socket closes at cleanup.
func subscribe(t testing.TB, c *gw.Client, ip, topic string, who tenancy.Cred, q url.Values) *sub {
	t.Helper()
	if q == nil {
		q = url.Values{}
	}
	q.Set("topic", topic)
	conn, resp, err := dial(t, c, ip, pathWS+"?"+q.Encode(), who)
	if err != nil {
		t.Fatalf("subscribing to %q failed (HTTP %v): %v", topic, statusOf(resp), err)
	}
	s := &sub{conn: conn, frames: make(chan frame, frameBuffer), closed: make(chan error, 1)}
	t.Cleanup(func() { _ = conn.Close() })
	go s.read()
	return s
}

func statusOf(resp *http.Response) any {
	if resp == nil {
		return "none"
	}
	return resp.StatusCode
}

func (s *sub) read() {
	for {
		_, raw, err := s.conn.ReadMessage()
		if err != nil {
			s.closed <- err
			close(s.frames)
			return
		}
		var env struct {
			Data      string `json:"data"`
			Topic     string `json:"topic"`
			Timestamp int64  `json:"timestamp"`
		}
		if json.Unmarshal(raw, &env) != nil {
			continue
		}
		data, err := base64.StdEncoding.DecodeString(env.Data)
		if err != nil {
			continue
		}
		s.frames <- frame{Data: data, Topic: env.Topic, Timestamp: env.Timestamp}
	}
}

// next returns the next frame within budget, or false.
func (s *sub) next(t testing.TB, budget time.Duration) (frame, bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), budget)
	defer cancel()
	select {
	case f, ok := <-s.frames:
		return f, ok
	case <-ctx.Done():
		return frame{}, false
	}
}

// await reads frames until one carries want, failing after budget. It
// returns the frames that came before it.
func (s *sub) await(t testing.TB, want []byte, budget time.Duration) []frame {
	t.Helper()
	_, before := s.awaitFrame(t, want, budget)
	return before
}

// awaitFrame is await returning the frame that carried want as well.
func (s *sub) awaitFrame(t testing.TB, want []byte, budget time.Duration) (frame, []frame) {
	t.Helper()
	var before []frame
	deadline := time.Now().Add(budget)
	for {
		f, ok := s.next(t, time.Until(deadline))
		if !ok {
			t.Fatalf("no frame carrying %q within %s (saw %d others)", truncate(want), budget, len(before))
		}
		if bytes.Equal(f.Data, want) {
			return f, before
		}
		before = append(before, f)
	}
}

// settle reads whatever else arrives during window and returns it: a frame
// that is late, or a duplicate, shows up here instead of going unseen after
// the last one the test waited for.
func (s *sub) settle(t testing.TB, window time.Duration) []frame {
	t.Helper()
	var late []frame
	deadline := time.Now().Add(window)
	for {
		f, ok := s.next(t, time.Until(deadline))
		if !ok {
			return late
		}
		late = append(late, f)
	}
}

// gather counts the frames by payload until done reports the counts complete
// (failing after budget), then keeps counting for settleWindow, so a duplicate
// that trails the last message is counted too.
func (s *sub) gather(t testing.TB, done func(map[string]int) bool, budget time.Duration) map[string]int {
	t.Helper()
	seen := map[string]int{}
	deadline := time.Now().Add(budget)
	for !done(seen) {
		f, ok := s.next(t, time.Until(deadline))
		if !ok {
			t.Fatalf("the messages did not all arrive within %s: %v", budget, seen)
		}
		seen[string(f.Data)]++
	}
	for _, f := range s.settle(t, settleWindow) {
		seen[string(f.Data)]++
	}
	return seen
}

// publish sends one message through c.
func publish(t testing.TB, c *gw.Client, who tenancy.Cred, topic string, data []byte) *gw.Response {
	t.Helper()
	return tenancy.Post(t, c, pathPublish, who, map[string]string{"topic": topic, "data_base64": base64.StdEncoding.EncodeToString(data)})
}

// warmUp publishes probes through pub until s receives one: a new
// subscription takes a moment to join the mesh another node publishes into.
// Waiting for that readiness signal is not a retry of the assertion; the
// assertion is made afterwards with a fresh message.
func warmUp(t testing.TB, pub *gw.Client, who tenancy.Cred, s *sub, topic string) {
	t.Helper()
	i := 0
	eventually.Require(t, pollEvery, meshBudget, "the subscription on "+topic+" to be reachable", func() (bool, error) {
		i++
		probe := []byte(fmt.Sprintf("warmup-%d", i))
		if resp := publish(t, pub, who, topic, probe); resp.Status != http.StatusOK {
			return false, eventually.Stop(fmt.Errorf("warm-up publish answered %d: %s", resp.Status, resp.Body))
		}
		for {
			f, ok := s.next(t, pollEvery)
			if !ok {
				return false, fmt.Errorf("probe %d not delivered yet", i)
			}
			if strings.HasPrefix(string(f.Data), "warmup-") {
				return true, nil
			}
		}
	})
	s.drain(t)
}

// drain discards whatever is buffered, waiting briefly for stragglers.
func (s *sub) drain(t testing.TB) {
	t.Helper()
	for {
		if _, ok := s.next(t, pollEvery); !ok {
			return
		}
	}
}

func truncate(b []byte) string {
	const max = 64
	if len(b) <= max {
		return string(b)
	}
	return string(b[:max]) + "…"
}
