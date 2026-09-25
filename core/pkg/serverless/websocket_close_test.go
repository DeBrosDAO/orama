package serverless

import (
	"errors"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

// recordingWSConn records how it was ended.
type recordingWSConn struct {
	fakeWSConn
	controlType int
	controlData []byte
	closed      bool
	controlErr  error
}

func (c *recordingWSConn) WriteControl(t int, data []byte, _ time.Time) error {
	c.controlType, c.controlData = t, data
	return c.controlErr
}

func (c *recordingWSConn) Close() error {
	c.closed = true
	return nil
}

func TestWSManagerCloseClient_sendsTheCodeThenCloses(t *testing.T) {
	m := NewWSManager(zap.NewNop())
	conn := &recordingWSConn{}
	m.Register("c1", conn)

	if err := m.CloseClient("c1", 4403, "session revoked"); err != nil {
		t.Fatalf("CloseClient: %v", err)
	}
	if conn.controlType != websocket.CloseMessage {
		t.Errorf("sent control frame type %d, want a close frame", conn.controlType)
	}
	if want := websocket.FormatCloseMessage(4403, "session revoked"); string(conn.controlData) != string(want) {
		t.Errorf("close frame %q, want %q", conn.controlData, want)
	}
	if !conn.closed {
		t.Error("the connection was left open")
	}
}

// The connection is what ends the socket; a client that did not read the close
// frame is still disconnected.
func TestWSManagerCloseClient_closesEvenWhenTheFrameFails(t *testing.T) {
	m := NewWSManager(zap.NewNop())
	conn := &recordingWSConn{controlErr: errors.New("write: broken pipe")}
	m.Register("c1", conn)

	if err := m.CloseClient("c1", 4401, "token expired"); err != nil {
		t.Fatalf("CloseClient: %v", err)
	}
	if !conn.closed {
		t.Error("a failed close frame left the connection open")
	}
}

func TestWSManagerCloseClient_unknownClient(t *testing.T) {
	m := NewWSManager(zap.NewNop())
	if err := m.CloseClient("nobody", 4401, "x"); !errors.Is(err, ErrWSClientNotFound) {
		t.Errorf("CloseClient on an unknown client returned %v, want ErrWSClientNotFound", err)
	}
}
