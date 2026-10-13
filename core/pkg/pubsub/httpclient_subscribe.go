package pubsub

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"go.uber.org/zap"
)

// stream is the one upstream subscription the client holds for a namespace
// and topic. Every handler subscribed to that key is fed from it; it is closed
// when the last handler leaves.
type stream struct {
	ctx    context.Context // the stream's own; not derived from any subscriber's
	cancel context.CancelFunc

	// ready closes once the service has answered the subscribe request; err is
	// set, before that, when it did not. Both are written only by the opener.
	ready chan struct{}
	err   error

	// Guarded by HTTPClient.mu.
	handlers map[uint64]MessageHandler
	anon     []uint64 // ids of handlers added by Subscribe, oldest first
	nextID   uint64
}

// Subscribe registers handler on the topic and returns once the service holds
// the subscription. Any number of handlers may share a topic; each Subscribe
// is paired with one Unsubscribe, which removes the most recent of them. A
// caller that needs to remove its own handler uses SubscribeHandle.
func (c *HTTPClient) Subscribe(ctx context.Context, topic string, handler MessageHandler) error {
	_, err := c.subscribe(ctx, topic, handler, true)
	return err
}

// SubscribeHandle registers handler on the topic and returns once the service
// holds the subscription. The returned function removes exactly this handler
// (a second call does nothing) and closes the upstream stream when it was the
// last one.
func (c *HTTPClient) SubscribeHandle(ctx context.Context, topic string, handler MessageHandler) (func() error, error) {
	return c.subscribe(ctx, topic, handler, false)
}

func (c *HTTPClient) subscribe(ctx context.Context, topic string, handler MessageHandler, anon bool) (func() error, error) {
	ns := c.ns(ctx)
	key := ns + "." + topic

	c.mu.Lock()
	st, exists := c.streams[key]
	if !exists {
		st = &stream{ready: make(chan struct{}), handlers: make(map[uint64]MessageHandler)}
		st.ctx, st.cancel = context.WithCancel(context.Background())
		c.streams[key] = st
	}
	id := st.nextID
	st.nextID++
	st.handlers[id] = handler
	if anon {
		st.anon = append(st.anon, id)
	}
	c.mu.Unlock()

	if !exists {
		// Not inline: the opener waits on the service for up to requestTimeout,
		// and the first subscriber's context must end its own wait like any
		// other's. If every subscriber leaves meanwhile, release cancels the
		// stream and the opener unwinds.
		go c.open(st, key, ns, topic)
	}
	select {
	case <-st.ready:
	case <-ctx.Done():
		c.release(key, st, id)
		return nil, fmt.Errorf("pubsub subscribe to %q in namespace %q: %w", topic, ns, ctx.Err())
	}
	if st.err != nil {
		return nil, fmt.Errorf("pubsub subscribe to %q in namespace %q: %w", topic, ns, st.err)
	}
	return func() error {
		c.release(key, st, id)
		return nil
	}, nil
}

// open asks the service for the stream and, once it answers, starts feeding
// the stream's handlers. It always closes st.ready; on failure it removes st
// from the client so the next subscriber opens a fresh one.
func (c *HTTPClient) open(st *stream, key, ns, topic string) {
	fail := func(err error) {
		st.cancel()
		c.mu.Lock()
		if c.streams[key] == st {
			delete(c.streams, key)
		}
		c.mu.Unlock()
		st.err = err
		close(st.ready)
	}

	u := fmt.Sprintf("%s/subscribe?namespace=%s&topic=%s", c.baseURL, url.QueryEscape(ns), url.QueryEscape(topic))
	req, err := http.NewRequestWithContext(st.ctx, http.MethodGet, u, nil)
	if err != nil {
		fail(fmt.Errorf("build request: %w", err))
		return
	}
	// The service answers only once it holds the subscription; a stream has no
	// overall timeout, the wait for that answer does.
	answerTimer := time.AfterFunc(requestTimeout, st.cancel)
	resp, err := (&http.Client{Transport: c.transport}).Do(req)
	answered := answerTimer.Stop()
	if err != nil {
		if !answered {
			err = fmt.Errorf("no answer from the pubsub service within %s: %w", requestTimeout, err)
		}
		fail(err)
		return
	}
	if resp.StatusCode != http.StatusOK {
		slurp, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		resp.Body.Close()
		fail(fmt.Errorf("%s %s", resp.Status, strings.TrimSpace(string(slurp))))
		return
	}
	close(st.ready)
	go c.feed(st, key, topic, resp.Body)
}

// feed delivers every event of the stream to the handlers subscribed to it,
// until the stream ends.
func (c *HTTPClient) feed(st *stream, key, topic string, body io.ReadCloser) {
	defer body.Close()
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(line, "data: "))
		if err != nil {
			c.logger.Warn("dropping a subscribe event that is not base64",
				zap.String("key", key), zap.Error(err))
			continue
		}
		c.mu.Lock()
		handlers := make([]MessageHandler, 0, len(st.handlers))
		for _, h := range st.handlers {
			handlers = append(handlers, h)
		}
		c.mu.Unlock()
		for _, h := range handlers {
			if err := h(topic, raw); err != nil {
				c.logger.Warn("subscribe handler failed", zap.String("key", key), zap.Error(err))
			}
		}
	}

	// The stream ended. When it was closed here (last handler left, Close) the
	// stream is already unregistered; otherwise the service or the socket ended
	// it, and this client must not keep advertising a stream that is gone.
	c.mu.Lock()
	dead := c.streams[key] == st
	if dead {
		delete(c.streams, key)
	}
	c.mu.Unlock()
	if dead {
		st.cancel()
		c.logger.Warn("subscribe stream ended; its handlers no longer receive",
			zap.String("key", key), zap.Error(sc.Err()))
	}
}

// release removes handler id from the stream and closes the stream when it was
// the last one. It does nothing for a handler that is already gone.
func (c *HTTPClient) release(key string, st *stream, id uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, held := st.handlers[id]; !held {
		return
	}
	delete(st.handlers, id)
	for i, a := range st.anon {
		if a == id {
			st.anon = append(st.anon[:i], st.anon[i+1:]...)
			break
		}
	}
	if len(st.handlers) == 0 && c.streams[key] == st {
		delete(c.streams, key)
		st.cancel()
	}
}

// Unsubscribe undoes the most recent Subscribe on the topic that has not been
// undone; it does nothing when there is none.
func (c *HTTPClient) Unsubscribe(ctx context.Context, topic string) error {
	key := c.ns(ctx) + "." + topic
	c.mu.Lock()
	st := c.streams[key]
	var id uint64
	found := false
	if st != nil && len(st.anon) > 0 {
		id, found = st.anon[len(st.anon)-1], true
	}
	c.mu.Unlock()
	if found {
		c.release(key, st, id)
	}
	return nil
}
