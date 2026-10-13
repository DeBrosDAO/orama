//go:build e2e_fleet

package soak

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/DeBrosOfficial/network/e2e/features/internal/realistic"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

const (
	soakTopic = "soak.feed"
	// closeExpired is the close code of a socket whose token expired more
	// than two minutes ago (docs/whitepaper/technical-reference/vol1/13-identity.md "Open WebSockets"): a client
	// reconnects with its fresh token, and so does this one.
	closeExpired = 4401
	// joinGrace is how long a new subscription may take to be reachable
	// from every node's publishes (GossipSub over the overlay).
	joinGrace = 30 * time.Second
	// deliverGrace is how long a message may be in flight.
	deliverGrace = 10 * time.Second
	socketPoll   = time.Second
	maxSoak      = 24 * time.Hour
)

// conn is one socket's life.
type conn struct {
	Open, Close time.Time
	Code        int // 0 while open or for a close without a close frame
	Err         string
}

// subscriber is a user's subscription through one node, reconnected like a
// client SDK does, remembering every message sequence it received.
type subscriber struct {
	name  string
	c     *gw.Client
	user  *realistic.User
	mu    sync.Mutex
	got   map[int64]bool
	conns []conn
}

// run keeps the subscription open until ctx ends.
func (s *subscriber) run(ctx context.Context) {
	var sock *realistic.Socket
	_ = eventually.Poll(ctx, socketPoll, maxSoak, "the "+s.name+" subscription", func() (bool, error) {
		if sock == nil {
			sock = s.open(ctx)
			return false, nil
		}
		s.read(sock)
		if sock.Closed() {
			s.closed(sock.Err())
			sock = nil
		}
		return false, nil
	})
	if sock != nil {
		sock.Close()
		s.closed(nil)
	}
}

func (s *subscriber) open(ctx context.Context) *realistic.Socket {
	sock, err := realistic.Subscribe(ctx, s.c, soakTopic, s.user.Token(), nil)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if err != nil {
		s.conns = append(s.conns, conn{Open: now, Close: now, Code: -1, Err: err.Error()})
		return nil
	}
	s.conns = append(s.conns, conn{Open: now})
	return sock
}

func (s *subscriber) read(sock *realistic.Socket) {
	for _, f := range sock.Drain() {
		var m struct {
			Seq int64 `json:"seq"`
		}
		if json.Unmarshal(f.Data, &m) == nil && m.Seq > 0 {
			s.mu.Lock()
			s.got[m.Seq] = true
			s.mu.Unlock()
		}
	}
}

func (s *subscriber) closed(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	last := &s.conns[len(s.conns)-1]
	if !last.Close.IsZero() {
		return
	}
	last.Close = time.Now()
	var ce *websocket.CloseError
	if errors.As(err, &ce) {
		last.Code = ce.Code
	}
	if err != nil {
		last.Err = err.Error()
	}
}

// feed publishes numbered messages and remembers when each was accepted.
type feed struct {
	mu  sync.Mutex
	at  map[int64]time.Time
	seq int64
}

// publish is the feed's load op.
func (fd *feed) publish(w *workload) realistic.Op {
	return func(ctx context.Context, wk, _ int) error {
		fd.mu.Lock()
		fd.seq++
		seq := fd.seq
		fd.mu.Unlock()
		data := base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf(`{"seq":%d}`, seq)))
		_, err := w.ns.JSON(ctx, http.MethodPost, "/v1/pubsub/publish", w.user(wk).Token(), map[string]string{"topic": soakTopic, "data_base64": data}, nil)
		if err == nil {
			fd.mu.Lock()
			fd.at[seq] = time.Now()
			fd.mu.Unlock()
		}
		return err
	}
}

// startSubscribers opens one subscription per user, each through the node
// the user's index picks, and keeps them open until ctx ends.
func startSubscribers(ctx context.Context, w *workload, wg *sync.WaitGroup) []*subscriber {
	var subs []*subscriber
	nodes := w.tn.F.State.Nodes
	for i, u := range w.users {
		node := nodes[i%len(nodes)]
		s := &subscriber{name: node.Name + "/" + u.Wallet.Address()[:10], c: w.ns.PinTo(node.PublicIP), user: u, got: map[int64]bool{}}
		subs = append(subs, s)
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.run(ctx)
		}()
	}
	return subs
}
