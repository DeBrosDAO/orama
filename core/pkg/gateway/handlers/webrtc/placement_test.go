package webrtc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/logging"
)

// fakeSFU answers /health?room= the way core/pkg/sfu does.
type fakeSFU struct {
	node     SFUNode
	srv      *httptest.Server
	mu       sync.Mutex
	draining bool
	rooms    map[string]bool
}

func newFakeSFU(t *testing.T, id string) *fakeSFU {
	t.Helper()
	f := &fakeSFU{rooms: map[string]bool{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.draining {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, `{"status":"draining","rooms":0,"hasRoom":false}`)
			return
		}
		fmt.Fprintf(w, `{"status":"ok","rooms":%d,"hasRoom":%t}`, len(f.rooms), f.rooms[r.URL.Query().Get("room")])
	}))
	t.Cleanup(f.srv.Close)
	host, port, err := net.SplitHostPort(f.srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	p, _ := strconv.Atoi(port)
	f.node = SFUNode{NodeID: id, Host: host, Port: p}
	return f
}

func (f *fakeSFU) setDraining(v bool) { f.mu.Lock(); f.draining = v; f.mu.Unlock() }
func (f *fakeSFU) host(room string)   { f.mu.Lock(); f.rooms[room] = true; f.mu.Unlock() }

type staticDirectory struct {
	nodes []SFUNode
	err   error
}

func (d staticDirectory) SFUNodes(context.Context, string) ([]SFUNode, error) { return d.nodes, d.err }

// gatewayFor builds the signal handler of one gateway node over a shared
// directory and reports the SFU each socket it proxies is sent to.
func gatewayFor(dir SFUDirectory, target *string) *WebRTCHandlers {
	logger, _ := logging.NewColoredLogger(logging.ComponentGeneral, false)
	h := NewWebRTCHandlers(logger, "10.0.0.9", 30000, "", "", func(w http.ResponseWriter, r *http.Request, targetHost string) bool {
		*target = targetHost
		return true
	})
	h.SetSFUDirectory(dir)
	return h
}

func signalTo(h *WebRTCHandlers, ns, room string) *httptest.ResponseRecorder {
	req := requestWithNamespace("GET", "/v1/webrtc/signal?"+url.Values{"room": {room}}.Encode(), ns)
	w := httptest.NewRecorder()
	h.SignalHandler(w, req)
	return w
}

func names(nodes []SFUNode) []string {
	var out []string
	for _, n := range nodes {
		out = append(out, n.NodeID)
	}
	return out
}

func threeSFUs(t *testing.T) ([]*fakeSFU, staticDirectory) {
	t.Helper()
	sfus := []*fakeSFU{newFakeSFU(t, "node-a"), newFakeSFU(t, "node-b"), newFakeSFU(t, "node-c")}
	var nodes []SFUNode
	for _, s := range sfus {
		nodes = append(nodes, s.node)
	}
	return sfus, staticDirectory{nodes: nodes}
}

func sfuByAddr(sfus []*fakeSFU, addr string) *fakeSFU {
	for _, s := range sfus {
		if s.node.Addr() == addr {
			return s
		}
	}
	return nil
}

// --- ranking ---

func TestRankSFUNodes_sameInputSameOrderRegardlessOfListOrder(t *testing.T) {
	a := []SFUNode{{NodeID: "n1"}, {NodeID: "n2"}, {NodeID: "n3"}}
	b := []SFUNode{{NodeID: "n3"}, {NodeID: "n1"}, {NodeID: "n2"}}
	got, want := names(rankSFUNodes(a, "ns", "room")), names(rankSFUNodes(b, "ns", "room"))
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("order depends on input order: %v vs %v", got, want)
	}
}

func TestRankSFUNodes_spreadsRoomsAcrossNodes(t *testing.T) {
	nodes := []SFUNode{{NodeID: "n1"}, {NodeID: "n2"}, {NodeID: "n3"}}
	top := map[string]int{}
	for i := 0; i < 300; i++ {
		top[rankSFUNodes(nodes, "ns", fmt.Sprintf("room-%d", i))[0].NodeID]++
	}
	for _, n := range nodes {
		if top[n.NodeID] < 50 {
			t.Errorf("%s owns %d of 300 rooms: %v", n.NodeID, top[n.NodeID], top)
		}
	}
}

// Removing a node moves only the rooms that node ranked first.
func TestRankSFUNodes_removingNodeKeepsOtherRoomsInPlace(t *testing.T) {
	all := []SFUNode{{NodeID: "n1"}, {NodeID: "n2"}, {NodeID: "n3"}}
	without := []SFUNode{{NodeID: "n1"}, {NodeID: "n3"}}
	for i := 0; i < 200; i++ {
		room := fmt.Sprintf("room-%d", i)
		before := rankSFUNodes(all, "ns", room)[0].NodeID
		after := rankSFUNodes(without, "ns", room)[0].NodeID
		if before != "n2" && before != after {
			t.Fatalf("%s moved from %s to %s though n2 did not own it", room, before, after)
		}
	}
}

func TestRankSFUNodes_empty(t *testing.T) {
	if got := rankSFUNodes(nil, "ns", "r"); len(got) != 0 {
		t.Fatalf("ranked %v from no nodes", got)
	}
}

// --- pick ---

func TestPickSFUOwner(t *testing.T) {
	ranked := []SFUNode{{NodeID: "a"}, {NodeID: "b"}, {NodeID: "c"}}
	up := sfuStatus{healthy: true}
	live := sfuStatus{healthy: true, hasRoom: true}
	cases := []struct {
		name    string
		status  map[string]sfuStatus
		want    string
		wantErr error
	}{
		{"top-ranked healthy node when no room is live", map[string]sfuStatus{"a": up, "b": up, "c": up}, "a", nil},
		{"unhealthy top is skipped", map[string]sfuStatus{"b": up, "c": up}, "b", nil},
		{"a live room beats rank", map[string]sfuStatus{"a": up, "b": live, "c": up}, "b", nil},
		{"two live copies: first in rank", map[string]sfuStatus{"a": up, "b": live, "c": live}, "b", nil},
		{"draining node hosting the room is not chosen", map[string]sfuStatus{"a": {hasRoom: true}, "b": up, "c": up}, "b", nil},
		{"nothing healthy", map[string]sfuStatus{}, "", ErrNoHealthySFU},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := pickSFUOwner(ranked, c.status)
			if !errors.Is(err, c.wantErr) {
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}
			if got.NodeID != c.want {
				t.Fatalf("owner = %q, want %q", got.NodeID, c.want)
			}
		})
	}
}

// --- handler: two gateways, one room, one SFU ---

func TestSignalHandler_twoGatewaysSameRoomSameSFU(t *testing.T) {
	_, dir := threeSFUs(t)
	var targetA, targetB string
	gwA, gwB := gatewayFor(dir, &targetA), gatewayFor(dir, &targetB)

	for i := 0; i < 20; i++ {
		room := fmt.Sprintf("call-%d", i)
		if w := signalTo(gwA, "ns", room); w.Code != http.StatusOK {
			t.Fatalf("gateway A: status %d %s", w.Code, w.Body)
		}
		if w := signalTo(gwB, "ns", room); w.Code != http.StatusOK {
			t.Fatalf("gateway B: status %d %s", w.Code, w.Body)
		}
		if targetA != targetB {
			t.Fatalf("room %s split: gateway A -> %s, gateway B -> %s", room, targetA, targetB)
		}
	}
}

func TestSignalHandler_differentRoomsUseMoreThanOneSFU(t *testing.T) {
	_, dir := threeSFUs(t)
	var target string
	gw := gatewayFor(dir, &target)
	seen := map[string]bool{}
	for i := 0; i < 40; i++ {
		signalTo(gw, "ns", fmt.Sprintf("r-%d", i))
		seen[target] = true
	}
	if len(seen) < 2 {
		t.Fatalf("40 rooms all landed on %v", seen)
	}
}

func TestSignalHandler_proxiesToSFUSignalPath(t *testing.T) {
	_, dir := threeSFUs(t)
	var target string
	gw := gatewayFor(dir, &target)
	req := requestWithNamespace("GET", "/v1/webrtc/signal?room=r1", "ns")
	gw.SignalHandler(httptest.NewRecorder(), req)
	if req.URL.Path != "/ws/signal" || req.URL.Host != target || req.Host != target {
		t.Fatalf("request not rewritten to the SFU: path=%s host=%s Host=%s target=%s", req.URL.Path, req.URL.Host, req.Host, target)
	}
	if req.URL.Query().Get("room") != "r1" {
		t.Fatalf("the room query did not reach the SFU: %s", req.URL.RawQuery)
	}
}

// --- owner failure and recovery ---

func TestSignalHandler_ownerDownRehomesAllGatewaysToSameNode(t *testing.T) {
	sfus, dir := threeSFUs(t)
	var targetA, targetB string
	gwA, gwB := gatewayFor(dir, &targetA), gatewayFor(dir, &targetB)

	signalTo(gwA, "ns", "standup")
	owner := sfuByAddr(sfus, targetA)
	if owner == nil {
		t.Fatalf("target %s is not an SFU", targetA)
	}

	owner.srv.Close() // the owner node dies
	signalTo(gwA, "ns", "standup")
	signalTo(gwB, "ns", "standup")
	if targetA == owner.node.Addr() {
		t.Fatal("a join was still routed to the dead owner")
	}
	if targetA != targetB {
		t.Fatalf("after the owner died the room split: %s vs %s", targetA, targetB)
	}
}

func TestSignalHandler_drainingOwnerRehomes(t *testing.T) {
	sfus, dir := threeSFUs(t)
	var target string
	gw := gatewayFor(dir, &target)

	signalTo(gw, "ns", "standup")
	owner := sfuByAddr(sfus, target)
	owner.setDraining(true)
	signalTo(gw, "ns", "standup")
	if target == owner.node.Addr() {
		t.Fatal("a join was routed to a draining SFU")
	}
}

// A recovered top-ranked node must not take a room that is live elsewhere.
func TestSignalHandler_recoveredOwnerDoesNotStealLiveRoom(t *testing.T) {
	sfus, dir := threeSFUs(t)
	var target string
	gw := gatewayFor(dir, &target)

	signalTo(gw, "ns", "standup")
	owner := sfuByAddr(sfus, target)
	owner.setDraining(true)
	signalTo(gw, "ns", "standup")
	fallback := sfuByAddr(sfus, target)
	fallback.host("standup") // the call now lives on the fallback

	owner.setDraining(false) // the top-ranked node is back
	signalTo(gw, "ns", "standup")
	if target != fallback.node.Addr() {
		t.Fatalf("a live room moved from %s to the recovered node %s", fallback.node.Addr(), target)
	}
}

// A call that started on any SFU (as every one did before rooms were placed)
// is found and joined wherever it lives.
func TestSignalHandler_joinsRoomAlreadyLiveOnNonTopNode(t *testing.T) {
	sfus, dir := threeSFUs(t)
	var target string
	gw := gatewayFor(dir, &target)

	signalTo(gw, "ns", "legacy")
	top := sfuByAddr(sfus, target)
	for _, s := range sfus {
		if s != top {
			s.host("legacy")
			break
		}
	}
	signalTo(gw, "ns", "legacy")
	if target == top.node.Addr() {
		t.Fatalf("joined the top-ranked node %s though the call lives elsewhere", target)
	}
}

func TestSignalHandler_setChangeMovesOnlyRoomsOfRemovedNode(t *testing.T) {
	sfus, dir := threeSFUs(t)
	var target string
	gw := gatewayFor(dir, &target)

	before := map[string]string{}
	for i := 0; i < 30; i++ {
		room := fmt.Sprintf("r-%d", i)
		signalTo(gw, "ns", room)
		before[room] = target
	}
	removed := sfus[1]
	gw.SetSFUDirectory(staticDirectory{nodes: []SFUNode{sfus[0].node, sfus[2].node}})
	for room, was := range before {
		signalTo(gw, "ns", room)
		if was != removed.node.Addr() && target != was {
			t.Fatalf("%s moved from %s to %s though its owner is still in the set", room, was, target)
		}
		if target == removed.node.Addr() {
			t.Fatalf("%s still routed to the removed node", room)
		}
	}
}

// --- handler errors ---

func TestSignalHandler_missingRoomIs400(t *testing.T) {
	_, dir := threeSFUs(t)
	var target string
	w := signalTo(gatewayFor(dir, &target), "ns", "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if target != "" {
		t.Fatal("proxied a socket with no room")
	}
}

func TestSignalHandler_noSFUNodesIs503(t *testing.T) {
	var target string
	w := signalTo(gatewayFor(staticDirectory{}, &target), "ns", "r")
	if w.Code != http.StatusServiceUnavailable || target != "" {
		t.Fatalf("status = %d target = %q, want 503 and no proxy", w.Code, target)
	}
}

func TestSignalHandler_noHealthySFUIs503(t *testing.T) {
	sfus, dir := threeSFUs(t)
	for _, s := range sfus {
		s.setDraining(true)
	}
	var target string
	w := signalTo(gatewayFor(dir, &target), "ns", "r")
	if w.Code != http.StatusServiceUnavailable || target != "" {
		t.Fatalf("status = %d target = %q, want 503 and no proxy", w.Code, target)
	}
}

func TestSignalHandler_registryErrorIs503(t *testing.T) {
	var target string
	w := signalTo(gatewayFor(staticDirectory{err: errors.New("rqlite down")}, &target), "ns", "r")
	if w.Code != http.StatusServiceUnavailable || target != "" {
		t.Fatalf("status = %d target = %q, want 503 and no proxy", w.Code, target)
	}
}

func TestSignalHandler_noDirectoryIs503(t *testing.T) {
	logger, _ := logging.NewColoredLogger(logging.ComponentGeneral, false)
	h := NewWebRTCHandlers(logger, "", 30000, "", "", func(http.ResponseWriter, *http.Request, string) bool { return true })
	if w := signalTo(h, "ns", "r"); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
}

// An SFU from before the room parameter ignores it: no hasRoom, so placement is
// plain rank order.
func TestHTTPSFUProbe_oldSFUBodyMeansHealthyWithoutRoom(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"status":"ok","rooms":2}`)
	}))
	defer srv.Close()
	host, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	p, _ := strconv.Atoi(port)
	st := httpSFUProbe(srv.Client())(context.Background(), SFUNode{Host: host, Port: p}, "r")
	if !st.healthy || st.hasRoom {
		t.Fatalf("status = %+v, want healthy without room", st)
	}
}
