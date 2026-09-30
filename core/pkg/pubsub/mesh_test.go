package pubsub

import (
	"context"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p"
	libp2pps "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"go.uber.org/zap"
)

var loopback = netip.MustParsePrefix("127.0.0.0/8")

// node is one pubsub service's libp2p host, manager and mesh, as cmd/pubsub
// builds them, on loopback.
type node struct {
	mgr   *Manager
	mesh  *Mesh
	gater *OverlayGater
	sock  string
	id    peer.ID
}

func startNode(t *testing.T) node {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	gater := NewOverlayGater(loopback)
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"), libp2p.ConnectionGater(gater))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	gs, err := libp2pps.NewGossipSub(ctx, h, libp2pps.WithFloodPublish(true))
	if err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(gs, "", zap.NewNop())
	t.Cleanup(func() { _ = mgr.Close() })
	mesh := NewMesh(h, loopback, gater)

	sock := shortSocketPath(t)
	ln, err := ListenSocket(sock, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: Handler(mgr, zap.NewNop(), WithMesh(mesh))}
	go srv.Serve(ln)
	t.Cleanup(func() { _ = srv.Close() })
	return node{mgr: mgr, mesh: mesh, gater: gater, sock: sock, id: h.ID()}
}

// receives subscribes on n to topic (in namespace ns) and returns what it gets.
func (n node) receives(t *testing.T, ns, topic string) <-chan string {
	t.Helper()
	got := make(chan string, 16)
	ctx := WithNamespace(context.Background(), ns)
	if _, err := n.mgr.SubscribeHandle(ctx, topic, func(_ string, data []byte) error {
		got <- string(data)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return got
}

// Bug (stagenet e2e TestPubsub_crossNodeDelivery and four more): the pubsub
// services of two nodes had no connection to each other, so a message
// published through one node never reached a subscriber on another. Connecting
// them through the mesh API makes the message arrive.
func TestMesh_servicesOnTwoNodesDeliverToEachOther(t *testing.T) {
	a, b := startNode(t), startNode(t)
	got := b.receives(t, "ns", "room")
	a.receives(t, "ns", "room")

	publishAll := func(msg string) {
		ctx := WithNamespace(context.Background(), "ns")
		if err := a.mgr.Publish(ctx, "room", []byte(msg)); err != nil {
			t.Fatal(err)
		}
	}
	publishAll("before")
	select {
	case m := <-got:
		t.Fatalf("a message crossed between services that are not connected: %q", m)
	case <-time.After(500 * time.Millisecond):
	}

	b.gater.Allow(a.id) // b's own gateway has told its service about a
	ca := NewHTTPClient(a.sock, "ns", zap.NewNop())
	defer ca.Close()
	cb := NewHTTPClient(b.sock, "ns", zap.NewNop())
	defer cb.Close()
	self, err := cb.MeshSelf(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if self.PeerID != b.id.String() || len(self.Addrs) != 1 || !strings.HasSuffix(self.Addrs[0], "/p2p/"+b.id.String()) {
		t.Fatalf("self = %+v, want b's overlay address with its peer id", self)
	}
	res, err := ca.MeshConnect(context.Background(), self.Addrs)
	if err != nil {
		t.Fatal(err)
	}
	if res.Connected != 1 || len(res.Failed) != 0 {
		t.Fatalf("connect result = %+v, want 1 connected", res)
	}

	deadline := time.After(5 * time.Second)
	for {
		publishAll("after")
		select {
		case m := <-got:
			if m != "after" {
				t.Fatalf("received %q, want only what was published after connecting", m)
			}
			return
		case <-time.After(100 * time.Millisecond):
		case <-deadline:
			t.Fatal("a message published on a connected service never reached the other's subscriber")
		}
	}
}

func TestMesh_connectIsIdempotentAndSkipsSelf(t *testing.T) {
	a, b := startNode(t), startNode(t)
	bs, err := b.mesh.Self()
	if err != nil {
		t.Fatal(err)
	}
	as, err := a.mesh.Self()
	if err != nil {
		t.Fatal(err)
	}
	b.gater.Allow(a.id)
	addrs := append(append([]string{}, bs.Addrs...), as.Addrs...)
	for range 2 {
		res, err := a.mesh.ConnectPeers(context.Background(), addrs)
		if err != nil {
			t.Fatal(err)
		}
		if res.Connected != 1 || len(res.Failed) != 0 {
			t.Fatalf("result = %+v, want b connected and a's own address skipped", res)
		}
	}
}

func TestMesh_emptyListConnectsNothing(t *testing.T) {
	a := startNode(t)
	res, err := a.mesh.ConnectPeers(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Connected != 0 || len(res.Failed) != 0 {
		t.Fatalf("result = %+v, want nothing", res)
	}
}

func TestMesh_anUnreachablePeerIsReportedAndDoesNotStopTheOthers(t *testing.T) {
	a, b := startNode(t), startNode(t)
	bs, err := b.mesh.Self()
	if err != nil {
		t.Fatal(err)
	}
	dead := startNode(t)
	deadAddr, err := dead.mesh.Self()
	if err != nil {
		t.Fatal(err)
	}
	if err := dead.mesh.host.Close(); err != nil {
		t.Fatal(err)
	}
	b.gater.Allow(a.id)
	res, err := a.mesh.ConnectPeers(context.Background(), append(deadAddr.Addrs, bs.Addrs...))
	if err != nil {
		t.Fatal(err)
	}
	if res.Connected != 1 || len(res.Failed) != 1 || res.Failed[0].Addr != deadAddr.Addrs[0] {
		t.Fatalf("result = %+v, want b connected and the closed node reported", res)
	}
	if a.mesh.host.Network().Connectedness(b.id) != network.Connected {
		t.Fatal("b is not connected")
	}
}

// A bad registry row used to refuse the whole list, so one row stopped every
// node from adding any peer. It is reported in Failed and the valid entries are
// still dialled.
func TestMesh_aBadEntryIsReportedAndTheValidOnesAreStillDialled(t *testing.T) {
	a, b := startNode(t), startNode(t)
	b.gater.Allow(a.id)
	bs, err := b.mesh.Self()
	if err != nil {
		t.Fatal(err)
	}
	valid := bs.Addrs[0]
	cases := map[string]string{
		"not a multiaddr":    "nonsense",
		"no peer id":         "/ip4/127.0.0.1/tcp/4001",
		"outside the mesh":   "/ip4/8.8.8.8/tcp/4001/p2p/" + startNode(t).id.String(),
		"no tcp port":        "/ip4/127.0.0.1/udp/4001/p2p/" + startNode(t).id.String(),
		"dns name":           "/dns4/example.com/tcp/4001/p2p/" + startNode(t).id.String(),
		"two transport legs": "/ip4/127.0.0.1/tcp/4001/ip4/127.0.0.2/tcp/4001/p2p/" + startNode(t).id.String(),
	}
	for name, bad := range cases {
		t.Run(name, func(t *testing.T) {
			res, err := a.mesh.ConnectPeers(context.Background(), []string{bad, valid})
			if err != nil {
				t.Fatalf("a bad entry refused the whole list: %v", err)
			}
			if res.Connected != 1 || len(res.Failed) != 1 || res.Failed[0].Addr != bad {
				t.Fatalf("result = %+v, want the valid peer connected and %q reported", res, bad)
			}
			if a.mesh.host.Network().Connectedness(b.id) != network.Connected {
				t.Fatal("the valid entry was not dialled")
			}
		})
	}
}

func TestMesh_refusesMoreThanMaxPeers(t *testing.T) {
	a := startNode(t)
	addr := "/ip4/127.0.0.1/tcp/4001/p2p/" + startNode(t).id.String()
	addrs := make([]string, MaxMeshPeers+1)
	for i := range addrs {
		addrs[i] = addr
	}
	if _, err := a.mesh.ConnectPeers(context.Background(), addrs); err == nil {
		t.Fatal("accepted more peers than MaxMeshPeers")
	}
}

func TestMesh_selfNeedsAnOverlayAddress(t *testing.T) {
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	overlay := netip.MustParsePrefix("10.0.0.0/24")
	m := NewMesh(h, overlay, NewOverlayGater(overlay))
	if self, err := m.Self(); err == nil {
		t.Fatalf("a host listening outside the overlay advertised %+v", self)
	}
}

func TestMeshAPI_badRequestsAreClientErrors(t *testing.T) {
	a := startNode(t)
	c := NewHTTPClient(a.sock, "ns", zap.NewNop())
	defer c.Close()
	_, err := c.MeshConnect(context.Background(), make([]string, MaxMeshPeers+1))
	if err == nil || !strings.Contains(err.Error(), "400") {
		t.Fatalf("err = %v, want the service's 400 for a list over the limit", err)
	}
}

func TestMeshAPI_aServiceWithoutAMeshHasNoMeshRoutes(t *testing.T) {
	sock, _ := startAPI(t)
	c := NewHTTPClient(sock, "ns", zap.NewNop())
	defer c.Close()
	if _, err := c.MeshSelf(context.Background()); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("err = %v, want 404", err)
	}
}

func peerIDOf(t *testing.T, addr string) peer.ID {
	t.Helper()
	i := strings.LastIndex(addr, "/p2p/")
	id, err := peer.Decode(addr[i+len("/p2p/"):])
	if err != nil {
		t.Fatal(fmt.Errorf("decode peer id in %q: %w", addr, err))
	}
	return id
}
