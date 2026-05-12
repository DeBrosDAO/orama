package wsbridge

import (
	"context"
	"fmt"
	"sync"

	"github.com/DeBrosOfficial/network/pkg/pubsub"
	"go.uber.org/zap"
)

// MaxTopicsPerClient bounds how many topics a single WS client can be
// bridged to, preventing pathological memory growth from a buggy or
// malicious function. Tunable per-deployment if needed.
const MaxTopicsPerClient = 1000

// WSSender is what the bridge calls to push bytes back to a WS client.
// In production this is *serverless.WSManager.Send. The interface keeps
// wsbridge independent of the concrete WS layer for testability.
type WSSender interface {
	Send(clientID string, data []byte) error
}

// PubSubManager is the subset of the pubsub.Manager API that wsbridge needs.
// Used as an interface for testability.
type PubSubManager interface {
	Subscribe(ctx context.Context, topic string, handler pubsub.MessageHandler) error
	Unsubscribe(ctx context.Context, topic string) error
}

// Bridge wires PubSub topics to WebSocket clients per namespace.
// Reference-counted libp2p subscriptions: only one active sub per
// (namespace, topic) regardless of how many clients are bridged.
type Bridge struct {
	mu     sync.RWMutex
	perNS  map[string]*nsTable
	pubsub PubSubManager
	ws     WSSender
	logger *zap.Logger
}

// nsTable holds bridge state for one namespace.
type nsTable struct {
	mu sync.Mutex
	// topic → set of clientIDs subscribed via the bridge
	topicToClients map[string]map[string]struct{}
	// client → set of topics it's bridged to (for cleanup on disconnect)
	clientToTopics map[string]map[string]struct{}
	// active libp2p subscriptions (refcount = len(topicToClients[topic]))
	subscribed map[string]bool
}

// clientNS tracks which namespace owns each WS client. Set at WS upgrade
// time so the host call can verify "function namespace == client namespace".
type clientNSTable struct {
	mu sync.RWMutex
	// clientID → namespace
	m map[string]string
}

// New constructs a Bridge. Both pubsub and ws may be nil for tests; the
// host functions degrade to no-ops in that case.
func New(ps PubSubManager, ws WSSender, logger *zap.Logger) *Bridge {
	return &Bridge{
		perNS:  make(map[string]*nsTable),
		pubsub: ps,
		ws:     ws,
		logger: logger,
	}
}

// SetClientNamespace records which namespace owns a WS client. Called at
// WS upgrade by the gateway handler. Replaces any prior assignment.
func (b *Bridge) SetClientNamespace(clientID, namespace string) {
	cnsOnce.Do(initCNS)
	cns.mu.Lock()
	cns.m[clientID] = namespace
	cns.mu.Unlock()
}

// GetClientNamespace returns the namespace owning a WS client.
// Returns ("", false) if the client is unknown.
func (b *Bridge) GetClientNamespace(clientID string) (string, bool) {
	cnsOnce.Do(initCNS)
	cns.mu.RLock()
	ns, ok := cns.m[clientID]
	cns.mu.RUnlock()
	return ns, ok
}

// Add bridges a (clientID, topic) pair within `namespace`. Idempotent.
// First add per (namespace, topic) opens a libp2p subscription.
func (b *Bridge) Add(ctx context.Context, namespace, clientID, topic string) error {
	if namespace == "" || clientID == "" || topic == "" {
		return fmt.Errorf("wsbridge.Add: namespace, clientID, topic all required")
	}

	tbl := b.getOrCreateNS(namespace)
	tbl.mu.Lock()
	defer tbl.mu.Unlock()

	// Per-client cap.
	if topics, ok := tbl.clientToTopics[clientID]; ok {
		if _, dup := topics[topic]; dup {
			return nil // idempotent
		}
		if len(topics) >= MaxTopicsPerClient {
			return fmt.Errorf("wsbridge.Add: client %s exceeds max topics (%d)",
				clientID, MaxTopicsPerClient)
		}
	}

	if _, ok := tbl.topicToClients[topic]; !ok {
		tbl.topicToClients[topic] = make(map[string]struct{})
	}
	tbl.topicToClients[topic][clientID] = struct{}{}

	if _, ok := tbl.clientToTopics[clientID]; !ok {
		tbl.clientToTopics[clientID] = make(map[string]struct{})
	}
	tbl.clientToTopics[clientID][topic] = struct{}{}

	// First subscriber for this (ns, topic): open libp2p subscription.
	if !tbl.subscribed[topic] {
		if b.pubsub != nil {
			ns := namespace
			t := topic
			handler := func(msgTopic string, data []byte) error {
				b.forward(ns, t, data)
				return nil
			}
			if err := b.pubsub.Subscribe(ctx, topic, handler); err != nil {
				// Roll back the bookkeeping — caller should see the failure.
				delete(tbl.topicToClients[topic], clientID)
				if len(tbl.topicToClients[topic]) == 0 {
					delete(tbl.topicToClients, topic)
				}
				delete(tbl.clientToTopics[clientID], topic)
				if len(tbl.clientToTopics[clientID]) == 0 {
					delete(tbl.clientToTopics, clientID)
				}
				return fmt.Errorf("wsbridge.Add: pubsub subscribe %q: %w", topic, err)
			}
		}
		tbl.subscribed[topic] = true
	}
	return nil
}

// Remove unbridges (clientID, topic). Idempotent. When the last client
// unbridges a topic, the libp2p subscription is closed.
func (b *Bridge) Remove(ctx context.Context, namespace, clientID, topic string) error {
	if namespace == "" || clientID == "" || topic == "" {
		return fmt.Errorf("wsbridge.Remove: namespace, clientID, topic all required")
	}
	b.mu.RLock()
	tbl, ok := b.perNS[namespace]
	b.mu.RUnlock()
	if !ok {
		return nil
	}
	tbl.mu.Lock()
	defer tbl.mu.Unlock()
	return b.removeLocked(ctx, tbl, clientID, topic)
}

// RemoveClient drops all bridges for a client (called on WS disconnect).
// Cleans up any libp2p subscriptions that hit refcount zero.
func (b *Bridge) RemoveClient(ctx context.Context, clientID string) {
	cnsOnce.Do(initCNS)
	cns.mu.Lock()
	delete(cns.m, clientID)
	cns.mu.Unlock()

	b.mu.RLock()
	tables := make([]*nsTable, 0, len(b.perNS))
	for _, t := range b.perNS {
		tables = append(tables, t)
	}
	b.mu.RUnlock()

	for _, tbl := range tables {
		tbl.mu.Lock()
		topics := tbl.clientToTopics[clientID]
		if len(topics) == 0 {
			tbl.mu.Unlock()
			continue
		}
		topicList := make([]string, 0, len(topics))
		for t := range topics {
			topicList = append(topicList, t)
		}
		for _, t := range topicList {
			_ = b.removeLocked(ctx, tbl, clientID, t)
		}
		tbl.mu.Unlock()
	}
}

// removeLocked must be called with tbl.mu held.
func (b *Bridge) removeLocked(ctx context.Context, tbl *nsTable, clientID, topic string) error {
	clients, ok := tbl.topicToClients[topic]
	if !ok {
		return nil
	}
	if _, ok := clients[clientID]; !ok {
		return nil
	}
	delete(clients, clientID)
	delete(tbl.clientToTopics[clientID], topic)
	if len(tbl.clientToTopics[clientID]) == 0 {
		delete(tbl.clientToTopics, clientID)
	}
	if len(clients) == 0 {
		// Last subscriber — close libp2p sub.
		delete(tbl.topicToClients, topic)
		delete(tbl.subscribed, topic)
		if b.pubsub != nil {
			if err := b.pubsub.Unsubscribe(ctx, topic); err != nil {
				b.logger.Debug("wsbridge.Remove: pubsub unsubscribe ignored",
					zap.String("topic", topic),
					zap.Error(err))
			}
		}
	}
	return nil
}

// Stats holds gauges for metrics export.
type Stats struct {
	Namespaces       int
	ActiveTopics     int
	ActiveClients    int
	TotalBridges     int
}

// Stats returns a snapshot of bridge counts.
func (b *Bridge) Stats() Stats {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := Stats{Namespaces: len(b.perNS)}
	uniqueClients := make(map[string]struct{})
	for _, tbl := range b.perNS {
		tbl.mu.Lock()
		out.ActiveTopics += len(tbl.topicToClients)
		for cid, ts := range tbl.clientToTopics {
			uniqueClients[cid] = struct{}{}
			out.TotalBridges += len(ts)
		}
		tbl.mu.Unlock()
	}
	out.ActiveClients = len(uniqueClients)
	return out
}

// forward fans an inbound libp2p message out to all bridged clients on the
// given (namespace, topic). Direct send; if a client's WS is slow/closed
// the send returns an error which we log-and-drop (no per-message buffering
// in v1; revisit if metrics show drops).
func (b *Bridge) forward(namespace, topic string, data []byte) {
	b.mu.RLock()
	tbl, ok := b.perNS[namespace]
	b.mu.RUnlock()
	if !ok {
		return
	}
	tbl.mu.Lock()
	clients := tbl.topicToClients[topic]
	cidSlice := make([]string, 0, len(clients))
	for c := range clients {
		cidSlice = append(cidSlice, c)
	}
	tbl.mu.Unlock()

	if b.ws == nil {
		return
	}
	for _, cid := range cidSlice {
		if err := b.ws.Send(cid, data); err != nil {
			b.logger.Debug("wsbridge.forward: ws send failed (slow/closed client)",
				zap.String("client_id", cid),
				zap.String("topic", topic),
				zap.Error(err))
		}
	}
}

func (b *Bridge) getOrCreateNS(namespace string) *nsTable {
	b.mu.RLock()
	tbl, ok := b.perNS[namespace]
	b.mu.RUnlock()
	if ok {
		return tbl
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if tbl, ok := b.perNS[namespace]; ok {
		return tbl
	}
	tbl = &nsTable{
		topicToClients: make(map[string]map[string]struct{}),
		clientToTopics: make(map[string]map[string]struct{}),
		subscribed:     make(map[string]bool),
	}
	b.perNS[namespace] = tbl
	return tbl
}

// Package-level client→namespace registry shared across Bridge instances.
// Ws clients are gateway-global identifiers (UUIDs) so a single registry
// is fine.
var (
	cns     *clientNSTable
	cnsOnce sync.Once
)

func initCNS() {
	cns = &clientNSTable{m: make(map[string]string)}
}
