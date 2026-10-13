package sfu

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
)

// joinedPeer joins one client to a fresh test server and returns the client
// socket and the SFU-side peer.
func joinedPeer(t *testing.T, s *Server, roomID string) (*websocket.Conn, *Peer) {
	t.Helper()
	conn := dialSignal(t, s, "room="+roomID)
	sendJoin(t, conn, roomID)
	if m := readFrame(t, conn); m.Type != MessageTypeWelcome {
		t.Fatalf("first frame = %s, want welcome", m.Type)
	}
	return conn, serverPeer(t, s, roomID)
}

// ICE failing or timing out reconnection must release the PeerConnection and
// the socket, not only mark the peer closed (the bug: handleDisconnect set the
// closed flag, so the Close the room calls returned without doing anything).
func TestHandleDisconnect_releasesConnectionAndSocket(t *testing.T) {
	s := newTestServer(t)
	conn, peer := joinedPeer(t, s, "leak")
	room := s.roomManager.GetRoom("leak")

	peer.handleDisconnect()

	if got := room.GetParticipantCount(); got != 0 {
		t.Errorf("room still has %d peers after the disconnect", got)
	}
	if got := peer.pc.ConnectionState(); got != webrtc.PeerConnectionStateClosed {
		t.Errorf("PeerConnection state = %s, want closed", got)
	}
	select {
	case <-peer.done:
	default:
		t.Error("done is not closed: goroutines tied to the peer keep running")
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Error("the client socket is still open after the disconnect")
	}
}

// The room had already dropped the peer (it left, then ICE failed): the
// resources must still be released.
func TestHandleDisconnect_afterRoomRemovedThePeer(t *testing.T) {
	s := newTestServer(t)
	_, peer := joinedPeer(t, s, "gone")
	room := s.roomManager.GetRoom("gone")

	room.peersMu.Lock()
	delete(room.peers, peer.ID)
	room.peersMu.Unlock()
	peer.handleDisconnect()

	if got := peer.pc.ConnectionState(); got != webrtc.PeerConnectionStateClosed {
		t.Errorf("PeerConnection state = %s, want closed", got)
	}
}

func TestPeerClose_concurrentAndRepeatedCallsCloseOnce(t *testing.T) {
	s := newTestServer(t)
	_, peer := joinedPeer(t, s, "once")

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); peer.Close() }()
		go func() { defer wg.Done(); peer.handleDisconnect() }()
	}
	wg.Wait()

	if err := peer.Close(); err != nil {
		t.Errorf("Close after Close = %v, want the first result (nil)", err)
	}
	if got := peer.SendMessage(NewServerMessage(MessageTypeError, &ErrorData{})); got != ErrPeerClosed {
		t.Errorf("SendMessage on a closed peer = %v, want ErrPeerClosed", got)
	}
}

func TestPeerClose_withoutConnectionOrSocket(t *testing.T) {
	p := NewPeer("u", nil, nil, testLogger())
	if err := p.Close(); err != nil {
		t.Errorf("Close = %v, want nil", err)
	}
	if err := p.SendMessage(NewServerMessage(MessageTypeError, &ErrorData{})); err != ErrPeerClosed {
		t.Errorf("SendMessage = %v, want ErrPeerClosed", err)
	}
}

func TestSendMessage_withoutSocket(t *testing.T) {
	p := NewPeer("u", nil, nil, testLogger())
	if err := p.SendMessage(NewServerMessage(MessageTypeError, &ErrorData{})); err != ErrWebSocketClosed {
		t.Errorf("SendMessage = %v, want ErrWebSocketClosed", err)
	}
}

// goroutinesIn counts the goroutines whose stack mentions fn.
func goroutinesIn(fn string) int {
	buf := make([]byte, 1<<22)
	return strings.Count(string(buf[:runtime.Stack(buf, true)]), fn)
}

// Peers that join and leave must take their goroutines with them: the TURN
// credential loops used to sleep through their timers after the peer was gone.
func TestPeerLeave_endsItsGoroutines(t *testing.T) {
	s := newTestServer(t)
	s.config.TURNServers = testConfig().TURNServers // the refresh loops only run with TURN
	s.config.TURNSecret = testConfig().TURNSecret

	const clients = 5
	var conns []*websocket.Conn
	for i := 0; i < clients; i++ {
		conn := dialSignal(t, s, "room=churn")
		sendJoin(t, conn, "churn")
		readFrame(t, conn)
		conns = append(conns, conn)
	}
	for _, loop := range []string{"credentialRefreshLoop", "turnRefreshLoop"} {
		waitFor(t, loop+" to run for every client", func() bool { return goroutinesIn(loop) >= clients })
	}

	for _, c := range conns {
		c.WriteJSON(ClientMessage{Type: MessageTypeLeave})
		c.Close()
	}
	for _, loop := range []string{"credentialRefreshLoop", "turnRefreshLoop"} {
		waitFor(t, loop+" goroutines to exit", func() bool { return goroutinesIn(loop) == 0 })
	}
}

// stallConn is a connection whose writes stop making progress once stalled is
// set: a write waits for its deadline and then fails, as one to a client that
// has stopped reading does once the kernel's buffers are full.
type stallConn struct {
	net.Conn
	stalled  atomic.Bool
	mu       sync.Mutex
	deadline time.Time
	closed   chan struct{}
	once     sync.Once
}

func (c *stallConn) SetWriteDeadline(t time.Time) error {
	c.mu.Lock()
	c.deadline = t
	c.mu.Unlock()
	return c.Conn.SetWriteDeadline(t)
}

func (c *stallConn) Write(b []byte) (int, error) {
	if !c.stalled.Load() {
		return c.Conn.Write(b)
	}
	c.mu.Lock()
	d := c.deadline
	c.mu.Unlock()
	select {
	case <-time.After(time.Until(d)):
		return 0, os.ErrDeadlineExceeded
	case <-c.closed:
		return 0, net.ErrClosed
	}
}

func (c *stallConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return c.Conn.Close()
}

type stallListener struct {
	net.Listener
	conns chan *stallConn
}

func (l *stallListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	sc := &stallConn{Conn: c, closed: make(chan struct{})}
	l.conns <- sc
	return sc, nil
}

// A client that stops reading must cost the room one disconnected peer, not a
// stalled broadcast.
func TestBroadcastMessage_stalledPeerIsDisconnectedOthersUnaffected(t *testing.T) {
	room := NewRoomManager(testConfig(), testLogger()).GetOrCreateRoom("stall")

	upgraded := make(chan *websocket.Conn, 2)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		upgraded <- c
	}))
	listener := &stallListener{Listener: srv.Listener, conns: make(chan *stallConn, 2)}
	srv.Listener = listener
	srv.Start()
	defer srv.Close()

	dial := func() (client, server *websocket.Conn, raw *stallConn) {
		c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
		if err != nil {
			t.Fatal(err)
		}
		return c, <-upgraded, <-listener.conns
	}
	stalledClient, stalledServer, stalledRaw := dial()
	healthyClient, healthyServer, _ := dial()
	defer stalledClient.Close()
	defer healthyClient.Close()
	stalledRaw.stalled.Store(true)

	stalled := NewPeer("stalled", stalledServer, room, testLogger())
	healthy := NewPeer("healthy", healthyServer, room, testLogger())
	for _, p := range []*Peer{stalled, healthy} {
		p.writeTimeout = 200 * time.Millisecond
		p.OnClose(func(p *Peer) { room.RemovePeer(p.ID) })
		room.peers[p.ID] = p
	}

	received := make(chan struct{}, 16)
	go func() {
		for {
			if _, _, err := healthyClient.ReadMessage(); err != nil {
				return
			}
			select {
			case received <- struct{}{}:
			default:
			}
		}
	}()

	started := time.Now()
	room.broadcastMessage("", NewServerMessage(MessageTypeServerDraining, &ServerDrainingData{Reason: "x"}))
	if took := time.Since(started); took > 3*time.Second {
		t.Errorf("a broadcast to one stalled peer took %s: the write is not bounded", took)
	}

	waitFor(t, "the stalled peer to be removed", func() bool { return room.GetParticipantCount() == 1 })
	room.peersMu.RLock()
	_, healthyStays := room.peers[healthy.ID]
	room.peersMu.RUnlock()
	if !healthyStays {
		t.Fatal("the healthy peer was removed along with the stalled one")
	}
	select {
	case <-received:
	case <-time.After(settleTimeout):
		t.Error("the healthy peer received nothing")
	}
	select {
	case <-stalled.done:
	default:
		t.Error("the stalled peer was removed from the room but not closed")
	}
}
