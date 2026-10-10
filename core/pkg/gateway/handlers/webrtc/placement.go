package webrtc

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"sync"
	"time"
)

// A room lives in exactly one SFU process, so every peer of a call must be
// routed to the same SFU whichever gateway node its socket lands on. Placement
// is computed, not stored: rank the namespace's healthy SFU nodes by
// rendezvous (highest-random-weight) hash of namespace|room|node, and prefer a
// node that already hosts the room. No shared-state write happens on the join
// path. See website/src/docs/operator/webrtc-operations.mdx#room-placement.

const (
	// sfuProbeTimeout bounds one health probe. The probe runs over WireGuard to a
	// process that answers from memory; a node that cannot answer this quickly
	// is not a node to put a call on.
	sfuProbeTimeout = 1500 * time.Millisecond

	// sfuProbeBodyLimit caps the health body read; the payload is a few dozen bytes.
	sfuProbeBodyLimit = 4096
)

// ErrNoSFUNodes means the namespace has no SFU registered.
var ErrNoSFUNodes = errors.New("namespace has no SFU nodes registered")

// ErrNoHealthySFU means SFU nodes are registered but none answered its health probe as ready.
var ErrNoHealthySFU = errors.New("no SFU node is healthy")

// SFUNode is one SFU process of a namespace, reachable over WireGuard.
type SFUNode struct {
	NodeID string
	Host   string // WireGuard IP
	Port   int    // signalling port
}

// Addr is the host:port the signalling socket is proxied to.
func (n SFUNode) Addr() string { return net.JoinHostPort(n.Host, strconv.Itoa(n.Port)) }

// SFUDirectory lists the SFU nodes a namespace's roles are allocated to.
type SFUDirectory interface {
	SFUNodes(ctx context.Context, namespace string) ([]SFUNode, error)
}

// sfuStatus is what one SFU reports for a room.
type sfuStatus struct {
	healthy bool // answered 200 (a draining SFU answers 503)
	hasRoom bool // the room has participants on this SFU
}

// sfuProber asks one SFU whether it is ready and whether it hosts room.
type sfuProber func(ctx context.Context, node SFUNode, room string) sfuStatus

// rankSFUNodes orders nodes by descending rendezvous weight for (namespace,
// room). The order depends only on the inputs, so every gateway computes the
// same one, and removing a node moves only the rooms that node ranked first.
func rankSFUNodes(nodes []SFUNode, namespace, room string) []SFUNode {
	type scored struct {
		node  SFUNode
		score uint64
	}
	all := make([]scored, len(nodes))
	for i, n := range nodes {
		sum := sha256.Sum256([]byte(namespace + "\x00" + room + "\x00" + n.NodeID))
		all[i] = scored{node: n, score: binary.BigEndian.Uint64(sum[:8])}
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].score != all[j].score {
			return all[i].score > all[j].score
		}
		return all[i].node.NodeID < all[j].node.NodeID
	})
	ranked := make([]SFUNode, len(all))
	for i, s := range all {
		ranked[i] = s.node
	}
	return ranked
}

// pickSFUOwner chooses the SFU for a join. Unhealthy nodes (down or draining)
// are skipped. Among the healthy ones, the first in rank that already hosts the
// room wins, so a call in progress is joined wherever it lives (a node that
// came back or was added does not steal a live room); with no live room the
// top-ranked healthy node wins, so concurrent first joins through different
// gateways agree.
func pickSFUOwner(ranked []SFUNode, status map[string]sfuStatus) (SFUNode, error) {
	var first *SFUNode
	for i := range ranked {
		st := status[ranked[i].NodeID]
		if !st.healthy {
			continue
		}
		if st.hasRoom {
			return ranked[i], nil
		}
		if first == nil {
			first = &ranked[i]
		}
	}
	if first == nil {
		return SFUNode{}, ErrNoHealthySFU
	}
	return *first, nil
}

// ownerOf resolves the SFU that owns room in namespace right now.
func (h *WebRTCHandlers) ownerOf(ctx context.Context, namespace, room string) (SFUNode, error) {
	if h.sfuDirectory == nil {
		return SFUNode{}, errors.New("SFU directory is not configured")
	}
	nodes, err := h.sfuDirectory.SFUNodes(ctx, namespace)
	if err != nil {
		return SFUNode{}, fmt.Errorf("failed to list SFU nodes for namespace %q: %w", namespace, err)
	}
	if len(nodes) == 0 {
		return SFUNode{}, ErrNoSFUNodes
	}
	ranked := rankSFUNodes(nodes, namespace, room)

	status := make(map[string]sfuStatus, len(ranked))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, n := range ranked {
		wg.Add(1)
		go func(n SFUNode) {
			defer wg.Done()
			st := h.probe(ctx, n, room)
			mu.Lock()
			status[n.NodeID] = st
			mu.Unlock()
		}(n)
	}
	wg.Wait()
	return pickSFUOwner(ranked, status)
}

// newSFUProbeClient returns the client for SFU health probes: direct to the
// WireGuard address (no environment proxy) and never following a redirect.
func newSFUProbeClient() *http.Client {
	return &http.Client{
		Transport:     &http.Transport{Proxy: nil},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// httpSFUProbe is the production sfuProber: GET /health?room=<id>. An SFU that
// predates the room parameter ignores it and reports no room, which degrades to
// plain rank order.
func httpSFUProbe(client *http.Client) sfuProber {
	return func(ctx context.Context, node SFUNode, room string) sfuStatus {
		ctx, cancel := context.WithTimeout(ctx, sfuProbeTimeout)
		defer cancel()
		target := "http://" + node.Addr() + "/health?room=" + url.QueryEscape(room)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return sfuStatus{}
		}
		resp, err := client.Do(req)
		if err != nil {
			return sfuStatus{}
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return sfuStatus{}
		}
		var body struct {
			HasRoom bool `json:"hasRoom"`
		}
		// A body without hasRoom (an SFU from before the parameter) means no room here.
		_ = json.NewDecoder(io.LimitReader(resp.Body, sfuProbeBodyLimit)).Decode(&body)
		return sfuStatus{healthy: true, hasRoom: body.HasRoom}
	}
}
