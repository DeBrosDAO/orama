package chainread

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// The head stream, GET /v1/chain/head/stream: the chain's head as server-sent events, so a page that
// wants to know when a block arrives does not ask for it every few seconds. The gateway reads the
// node's status once a second, however many pages are listening, and tells each of them when the
// height moves. Before this every open page made its own request every three seconds, so the node's
// load grew with the readers; now it grows with the gateways.
//
// The stream does not need the chain indexer: it reads the node's own status, so it serves a node
// that runs none. A browser's EventSource reconnects by itself, which is how a stream that ends at
// headStreamMaxAge is replaced, and how a gateway that restarts is found again.
const (
	headStreamPath = "head/stream"

	// headPollInterval is how often the shared reader asks the node for its status: a block is a
	// few seconds, so a new one is announced within a second of its arrival.
	headPollInterval = time.Second
	// headStreamMaxAge ends a stream before the server's 120 s write timeout, as the telemetry stream
	// does; the browser connects again.
	headStreamMaxAge = 100 * time.Second
	// headKeepalive keeps an idle proxy from closing a quiet stream.
	headKeepalive = 15 * time.Second
	// headRetry is how long, in milliseconds, a browser waits before it reconnects.
	headRetry = 1000
	// headStreamMaxConcurrent bounds the streams open on one gateway; each holds a goroutine and a
	// connection. A client address is limited in how fast it opens them by the gateway's own rate limit.
	headStreamMaxConcurrent = 1024
	// headStreamRetryAfter is the Retry-After, in seconds, of a stream refused for want of a slot.
	headStreamRetryAfter = "5"

	eventHead        = "head"
	eventUnavailable = "unavailable"
	unavailableText  = "the chain could not be read"
)

// headEvent is the head as the stream sends it.
type headEvent struct {
	Height  int64  `json:"height"`
	Time    string `json:"time"`
	ChainID string `json:"chain_id"`
}

// headMessage is one server-sent event.
type headMessage struct {
	name string
	data []byte
}

func (m headMessage) write(w io.Writer) error {
	_, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", m.name, m.data)
	return err
}

// headHub shares one reader of the node's status among the open streams. The reader runs while
// there is a stream to tell, and stops when the last one goes.
type headHub struct {
	read     func(ctx context.Context) (headEvent, error)
	interval time.Duration

	mu     sync.Mutex
	subs   map[chan headMessage]struct{}
	last   *headMessage
	height int64
	chain  string
	failed bool
	cancel context.CancelFunc
}

func newHeadHub(read func(ctx context.Context) (headEvent, error), interval time.Duration) *headHub {
	return &headHub{read: read, interval: interval, subs: map[chan headMessage]struct{}{}}
}

// subscribe adds a stream. The channel holds the latest message, which is the head the reader last
// saw if it has seen one. The returned function removes the stream and stops the reader if it was
// the last.
func (h *headHub) subscribe() (<-chan headMessage, func()) {
	ch := make(chan headMessage, 2)
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.last != nil {
		ch <- *h.last
	}
	if h.failed {
		ch <- unavailableMessage()
	}
	h.subs[ch] = struct{}{}
	if h.cancel == nil {
		ctx, cancel := context.WithCancel(context.Background())
		h.cancel = cancel
		go h.run(ctx)
	}
	return ch, func() { h.unsubscribe(ch) }
}

func (h *headHub) unsubscribe(ch chan headMessage) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.subs, ch)
	if len(h.subs) == 0 && h.cancel != nil {
		h.cancel()
		h.cancel, h.last, h.height, h.chain, h.failed = nil, nil, 0, "", false
	}
}

func (h *headHub) run(ctx context.Context) {
	tick := time.NewTicker(h.interval)
	defer tick.Stop()
	for {
		h.poll(ctx)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// poll reads the head once and tells the streams if it moved to a new height, or to another chain (a
// network that started again has a lower height). A read that fails is told once, until one works.
func (h *headHub) poll(ctx context.Context) {
	head, err := h.read(ctx)
	if ctx.Err() != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if ctx.Err() != nil {
		// The last stream left while the read was in flight: this reader belongs to a session that has
		// ended, and what it read must not become the next session's first head.
		return
	}
	if err != nil {
		if !h.failed {
			h.failed = true
			h.broadcast(unavailableMessage())
		}
		return
	}
	if !h.failed && h.last != nil && head.Height == h.height && head.ChainID == h.chain {
		return
	}
	h.failed, h.height, h.chain = false, head.Height, head.ChainID
	msg := headMessage{name: eventHead, data: mustJSON(head)}
	h.last = &msg
	h.broadcast(msg)
}

// broadcast gives msg to every stream. A stream that has not taken the last message loses it to this
// one: a head that has been replaced is not worth sending.
func (h *headHub) broadcast(msg headMessage) {
	for ch := range h.subs {
		select {
		case ch <- msg:
		default:
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- msg:
			default:
			}
		}
	}
}

func unavailableMessage() headMessage {
	return headMessage{name: eventUnavailable, data: mustJSON(map[string]string{"message": unavailableText})}
}

func mustJSON(v any) []byte {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("chainread: a head event could not be encoded: %v", err))
	}
	return raw
}

// readHead asks the node for its status and returns the head in it.
func (p *Proxy) readHead(ctx context.Context) (headEvent, error) {
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	u := *p.rpc
	u.Path = upstreamStatus
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return headEvent{}, fmt.Errorf("failed to build the status request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return headEvent{}, fmt.Errorf("failed to reach the node: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return headEvent{}, fmt.Errorf("the node answered status %d", resp.StatusCode)
	}
	var env struct {
		Result struct {
			NodeInfo struct {
				Network string `json:"network"`
			} `json:"node_info"`
			SyncInfo struct {
				Height string `json:"latest_block_height"`
				Time   string `json:"latest_block_time"`
			} `json:"sync_info"`
		} `json:"result"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, statusMaxBody)).Decode(&env); err != nil {
		return headEvent{}, fmt.Errorf("the node's status is not JSON: %w", err)
	}
	height, err := strconv.ParseInt(env.Result.SyncInfo.Height, 10, 64)
	if err != nil || height < 1 || env.Result.NodeInfo.Network == "" {
		return headEvent{}, fmt.Errorf("the node's status holds no head (height %q, chain %q)", env.Result.SyncInfo.Height, env.Result.NodeInfo.Network)
	}
	return headEvent{Height: height, Time: env.Result.SyncInfo.Time, ChainID: env.Result.NodeInfo.Network}, nil
}

// serveHeadStream answers GET /v1/chain/head/stream.
func (p *Proxy) serveHeadStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if r.URL.RawQuery != "" {
		writeErr(w, http.StatusBadRequest, "bad query")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming is not supported on this connection")
		return
	}
	select {
	case p.headSlots <- struct{}{}:
		defer func() { <-p.headSlots }()
	default:
		w.Header().Set("Retry-After", headStreamRetryAfter)
		writeErr(w, http.StatusServiceUnavailable, "too many head streams are open on this gateway; try again")
		return
	}
	messages, unsubscribe := p.heads.subscribe()
	defer unsubscribe()

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", cacheNoStore)
	h.Set("X-Accel-Buffering", "no")
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	if _, err := fmt.Fprintf(w, "retry: %d\n\n", headRetry); err != nil {
		return
	}
	flusher.Flush()
	p.pumpHead(r.Context(), w, flusher, messages)
}

// pumpHead writes the stream's messages until the browser goes, a write fails, or the stream is old.
func (p *Proxy) pumpHead(ctx context.Context, w io.Writer, f http.Flusher, messages <-chan headMessage) {
	deadline := time.NewTimer(p.headMaxAge)
	defer deadline.Stop()
	keepalive := time.NewTicker(headKeepalive)
	defer keepalive.Stop()
	for {
		var err error
		select {
		case <-ctx.Done():
			return
		case <-deadline.C:
			return
		case <-keepalive.C:
			_, err = fmt.Fprint(w, ": keepalive\n\n")
		case msg := <-messages:
			err = msg.write(w)
		}
		if err != nil {
			return
		}
		f.Flush()
	}
}
