package sfu

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/DeBrosOfficial/network/pkg/sfu/ctrlauth"
	"go.uber.org/zap"
)

// Membership reporting: every join and leave is posted, in order, to a
// namespace gateway, which publishes it on the namespace's pubsub
// (docs/WEBRTC.md#membership-events).
//
// The SFU has no pubsub of its own, and the gateway a socket came through is
// the one place that dies together with the socket: when that gateway restarts
// every peer behind it leaves at once, and their leave events could not reach
// it. So the reporter keeps the gateways it has seen tickets from and delivers
// to the most recently seen one that answers. A gateway that refuses an event
// (a bad MAC) is a misconfiguration, which another gateway would refuse too, so
// that is an error and not a reason to try the next.
const (
	// eventQueueSize bounds the events waiting for delivery. A full queue means
	// no gateway has answered for a long while; the event is dropped and the
	// loss logged, rather than the room's join path blocking on it.
	eventQueueSize = 1024

	// eventPostTimeout bounds one delivery attempt to one gateway.
	eventPostTimeout = 3 * time.Second

	// maxEventSinks bounds the gateways remembered.
	maxEventSinks = 8

	// eventSinkTTL is how long a gateway stays a delivery target after the last
	// ticket from it; a gateway that has moved or gone stops being tried.
	eventSinkTTL = 10 * time.Minute

	// reporterDrainTimeout bounds how long Close waits for queued events.
	reporterDrainTimeout = 3 * time.Second

	maxEventResponseBytes = 512
)

type seenSink struct {
	url  string
	seen time.Time
}

type queuedEvent struct {
	event ctrlauth.MembershipEvent
	body  []byte
}

// reporter delivers membership events from one queue, so a peer's join always
// precedes its leave.
type reporter struct {
	key    []byte
	client *http.Client
	logger *zap.Logger
	now    func() time.Time

	queue chan queuedEvent
	done  chan struct{}

	mu     sync.Mutex
	sinks  []seenSink // most recently seen first
	closed bool
}

func newReporter(key []byte, logger *zap.Logger) *reporter {
	r := &reporter{
		key:    key,
		client: &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: eventPostTimeout},
		logger: logger.With(zap.String("component", "membership-reporter")),
		now:    time.Now,
		queue:  make(chan queuedEvent, eventQueueSize),
		done:   make(chan struct{}),
	}
	go r.run()
	return r
}

// observe records that sink issued a ticket just now.
func (r *reporter) observe(sink string) {
	if sink == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	kept := []seenSink{{url: sink, seen: now}}
	for _, s := range r.sinks {
		if s.url != sink && now.Sub(s.seen) < eventSinkTTL && len(kept) < maxEventSinks {
			kept = append(kept, s)
		}
	}
	r.sinks = kept
}

// report queues an event. It never blocks.
func (r *reporter) report(ev ctrlauth.MembershipEvent) {
	body, err := json.Marshal(ev)
	if err != nil {
		r.logger.Error("Failed to encode a membership event", zap.String("room", ev.Room), zap.Error(err))
		return
	}
	// The send is under the lock close takes to close the queue, so it can
	// never be a send on a closed channel; it does not block, so holding the
	// lock costs nothing.
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	select {
	case r.queue <- queuedEvent{event: ev, body: body}:
	default:
		r.logger.Error("Membership event dropped: the delivery queue is full, so no namespace gateway has accepted events for a long while",
			zap.String("type", ev.Type), zap.String("room", ev.Room), zap.String("user_id", ev.UserID))
	}
}

func (r *reporter) run() {
	defer close(r.done)
	for q := range r.queue {
		r.deliver(q)
	}
}

// snapshotSinks returns the live sinks, most recent first.
func (r *reporter) snapshotSinks() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	var urls []string
	for _, s := range r.sinks {
		if now.Sub(s.seen) < eventSinkTTL {
			urls = append(urls, s.url)
		}
	}
	return urls
}

func (r *reporter) deliver(q queuedEvent) {
	sinks := r.snapshotSinks()
	if len(sinks) == 0 {
		r.logger.Error("Membership event not delivered: no namespace gateway has issued a ticket recently, so there is nowhere to report to",
			zap.String("type", q.event.Type), zap.String("room", q.event.Room))
		return
	}
	var lastErr error
	for _, sink := range sinks {
		refused, err := r.post(sink, q.body)
		if err == nil {
			return
		}
		lastErr = err
		if refused {
			break
		}
	}
	r.logger.Error("Membership event not delivered: check that a namespace gateway is running and that its TURN secret matches this SFU's",
		zap.String("type", q.event.Type), zap.String("room", q.event.Room),
		zap.String("user_id", q.event.UserID), zap.Error(lastErr))
}

// post sends one event to one gateway. refused is true when the gateway
// answered and said no, as opposed to not answering.
func (r *reporter) post(sink string, body []byte) (refused bool, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), eventPostTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sink+ctrlauth.EventsPath, bytes.NewReader(body))
	if err != nil {
		return true, fmt.Errorf("failed to build the event request for %s: %w", sink, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(ctrlauth.MACHeader, ctrlauth.Sign(r.key, sink, http.MethodPost, ctrlauth.EventsPath, body, r.now()))
	resp, err := r.client.Do(req)
	if err != nil {
		return false, fmt.Errorf("failed to post an event to %s: %w", sink, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 == 2 {
		return false, nil
	}
	detail, _ := io.ReadAll(io.LimitReader(resp.Body, maxEventResponseBytes))
	return resp.StatusCode < 500, fmt.Errorf("gateway %s answered %d to a membership event: %s", sink, resp.StatusCode, detail)
}

// close stops accepting events and waits for the queued ones to be delivered,
// up to reporterDrainTimeout.
func (r *reporter) close() {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.closed = true
	close(r.queue)
	r.mu.Unlock()
	select {
	case <-r.done:
	case <-time.After(reporterDrainTimeout):
		r.logger.Error("Membership events still undelivered at shutdown", zap.Int("queued", len(r.queue)))
	}
}
