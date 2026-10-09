package client

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

const maintenanceTestWait = 30 * time.Second

// freeLoopbackPort returns a TCP port nothing listens on.
func freeLoopbackPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// laterPeer is a bootstrap peer whose host is started after the client.
type laterPeer struct {
	key  crypto.PrivKey
	id   peer.ID
	port int
}

func newLaterPeer(t *testing.T) *laterPeer {
	t.Helper()
	key, _, err := crypto.GenerateEd25519Key(nil)
	if err != nil {
		t.Fatal(err)
	}
	id, err := peer.IDFromPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return &laterPeer{key: key, id: id, port: freeLoopbackPort(t)}
}

func (p *laterPeer) addr() string {
	return fmt.Sprintf("/ip4/127.0.0.1/tcp/%d/p2p/%s", p.port, p.id)
}

// start brings the peer's host up on the port its address names.
func (p *laterPeer) start(t *testing.T) host.Host {
	t.Helper()
	h, err := libp2p.New(libp2p.Identity(p.key), libp2p.ListenAddrStrings(fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", p.port)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	return h
}

func connectedTo(c *Client, id peer.ID) bool {
	return c.Host().Network().Connectedness(id) == network.Connected
}

func waitConnected(t *testing.T, c *Client, id peer.ID) {
	t.Helper()
	deadline := time.Now().Add(maintenanceTestWait)
	for time.Now().Before(deadline) {
		if connectedTo(c, id) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("client never connected to %s", id)
}

func maintenanceClient(t *testing.T, bootstrap ...string) *Client {
	t.Helper()
	old := peerRedialInterval
	peerRedialInterval = 50 * time.Millisecond
	t.Cleanup(func() { peerRedialInterval = old })
	cfg := DefaultClientConfig("peer-maintenance-test")
	cfg.BootstrapPeers = bootstrap
	cfg.DatabaseEndpoints = nil
	cfg.QuietMode = true
	nc, err := NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	c := nc.(*Client)
	t.Cleanup(func() { _ = c.Disconnect() })
	if err := c.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	return c
}

// The gateway's case: the bootstrap peer was not listening when the client
// started, and the client still ends up connected to it.
func TestPeerMaintenance_connectsToAPeerThatStartsLater(t *testing.T) {
	p := newLaterPeer(t)
	c := maintenanceClient(t, p.addr())
	if connectedTo(c, p.id) {
		t.Fatal("connected to a peer that is not running")
	}
	p.start(t)
	waitConnected(t, c, p.id)
}

// A node restart drops the connection to the gateway's bootstrap peer; the
// client dials it again once the peer is back.
func TestPeerMaintenance_reconnectsAfterThePeerRestarts(t *testing.T) {
	p := newLaterPeer(t)
	h := p.start(t)
	c := maintenanceClient(t, p.addr())
	waitConnected(t, c, p.id)

	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(maintenanceTestWait)
	for connectedTo(c, p.id) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if connectedTo(c, p.id) {
		t.Fatal("still connected to a closed peer")
	}
	p.start(t)
	waitConnected(t, c, p.id)
}

// Disconnect ends the loop; it returns even while a peer is unreachable.
func TestPeerMaintenance_disconnectStopsTheLoop(t *testing.T) {
	p := newLaterPeer(t)
	c := maintenanceClient(t, p.addr())
	done := c.peersDone
	if done == nil {
		t.Fatal("no maintenance loop for a client with a bootstrap peer")
	}
	if err := c.Disconnect(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(maintenanceTestWait):
		t.Fatal("the loop outlived Disconnect")
	}
	if c.stopPeers != nil {
		t.Error("Disconnect left the stop function set")
	}
}

// No bootstrap peer, or only self or garbage: nothing to maintain.
func TestPeerMaintenance_noLoopWithoutAPeerToDial(t *testing.T) {
	c := maintenanceClient(t)
	if c.peersDone != nil {
		t.Error("a loop runs for a client with no bootstrap peer")
	}
	self := c.Host().ID()
	targets := bootstrapTargets([]string{
		"not a multiaddr",
		"/ip4/127.0.0.1/tcp/4001",
		fmt.Sprintf("/ip4/127.0.0.1/tcp/4001/p2p/%s", self),
	}, self, zap.NewNop())
	if len(targets) != 0 {
		t.Errorf("targets = %v, want none", targets)
	}
}

// The swarm backs a failed peer off for 5s plus n^2 seconds and refuses dials
// meanwhile. A peer that was down for a while (many failed rounds) must still
// be connected soon after it is back, not after the backoff.
func TestPeerMaintenance_backoffDoesNotDelayAReturningPeer(t *testing.T) {
	p := newLaterPeer(t)
	c := maintenanceClient(t, p.addr())
	time.Sleep(2 * time.Second) // ~40 failed rounds at the test's 50ms interval
	p.start(t)
	start := time.Now()
	waitConnected(t, c, p.id)
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("connected %s after the peer came back; the swarm's dial backoff delayed it", took)
	}
}

// Connect already warned about a peer it could not reach, so the loop's first
// failed redial of it stays quiet; the loop speaks again when the peer is back.
func TestPeerMaintenance_doesNotWarnAgainForAPeerConnectMissed(t *testing.T) {
	old := peerRedialInterval
	peerRedialInterval = 20 * time.Millisecond
	t.Cleanup(func() { peerRedialInterval = old })
	h, err := libp2p.New(libp2p.NoListenAddrs)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	p := newLaterPeer(t)
	info, err := peer.AddrInfoFromString(p.addr())
	if err != nil {
		t.Fatal(err)
	}
	core, logs := observer.New(zap.InfoLevel)
	c := &Client{logger: zap.New(core)}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.maintainPeers(ctx, h, []peer.AddrInfo{*info})
	}()
	t.Cleanup(func() { cancel(); <-done })

	time.Sleep(500 * time.Millisecond) // ~25 failed rounds
	if n := logs.FilterMessage("Bootstrap peer is not connected, redialing until it answers").Len(); n != 0 {
		t.Fatalf("the loop warned %d times about a peer Connect had already reported", n)
	}
	p.start(t)
	deadline := time.Now().Add(maintenanceTestWait)
	for logs.FilterMessage("Bootstrap peer connected again").Len() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the loop never reported the peer back")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A peer that was connected when the loop started is not down; losing it is
// the loop's own transition and is logged once.
func TestPeerMaintenance_warnsOnceWhenAConnectedPeerIsLost(t *testing.T) {
	old := peerRedialInterval
	peerRedialInterval = 20 * time.Millisecond
	t.Cleanup(func() { peerRedialInterval = old })
	p := newLaterPeer(t)
	ph := p.start(t)
	h, err := libp2p.New(libp2p.NoListenAddrs)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	info, err := peer.AddrInfoFromString(p.addr())
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Connect(context.Background(), *info); err != nil {
		t.Fatal(err)
	}
	if down := unconnectedPeers(h, []peer.AddrInfo{*info}); len(down) != 0 {
		t.Fatalf("a connected peer is reported down: %v", down)
	}
	core, logs := observer.New(zap.InfoLevel)
	c := &Client{logger: zap.New(core)}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.maintainPeers(ctx, h, []peer.AddrInfo{*info})
	}()
	t.Cleanup(func() { cancel(); <-done })

	if err := ph.Close(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(maintenanceTestWait)
	for logs.FilterMessage("Bootstrap peer is not connected, redialing until it answers").Len() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the loop never warned that the peer was lost")
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)
	if n := logs.FilterMessage("Bootstrap peer is not connected, redialing until it answers").Len(); n != 1 {
		t.Fatalf("the loop warned %d times for one lost peer, want 1", n)
	}
}

func TestUnconnectedPeers_emptyTargets(t *testing.T) {
	h, err := libp2p.New(libp2p.NoListenAddrs)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	if down := unconnectedPeers(h, nil); len(down) != 0 {
		t.Fatalf("down = %v for no targets", down)
	}
}
