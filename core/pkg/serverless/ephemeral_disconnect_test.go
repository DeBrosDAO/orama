package serverless

import (
	"context"
	"testing"

	"go.uber.org/zap"
)

// fakeWSConn is a no-op WebSocketConn for exercising WSManager lifecycle.
type fakeWSConn struct{}

func (fakeWSConn) WriteMessage(int, []byte) error    { return nil }
func (fakeWSConn) ReadMessage() (int, []byte, error) { return 0, nil, nil }
func (fakeWSConn) Close() error                      { return nil }

// TestWSManager_DisconnectHookClearsEphemeralState verifies the wiring that
// makes Feature #710's auto-clear work: a disconnect hook registered on the
// WSManager fires on Unregister, clearing the disconnecting client's ephemeral
// state. Both the stateless and persistent WS handlers call Unregister, so
// this single hook covers both paths.
func TestWSManager_DisconnectHookClearsEphemeralState(t *testing.T) {
	logger := zap.NewNop()
	wsm := NewWSManager(logger)
	pub := &capturePublisher{}
	store := NewEphemeralStore(pub.publish)

	// Wire the hook exactly as NewHostFunctions does.
	wsm.AddDisconnectHook(func(clientID string) {
		store.ClearClient(context.Background(), clientID)
	})

	clientID := "client-A"
	wsm.Register(clientID, fakeWSConn{})

	if err := store.Set(context.Background(), "ns1", clientID, "t", "k", []byte("p"), 0); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if store.keyCountForTest() != 1 {
		t.Fatalf("expected 1 key before disconnect, got %d", store.keyCountForTest())
	}

	// Disconnect → hook fires → state cleared + synthetic clear published.
	wsm.Unregister(clientID)

	if store.keyCountForTest() != 0 {
		t.Errorf("disconnect hook did not clear ephemeral state, count=%d", store.keyCountForTest())
	}
	if pub.countKind(EphemeralEventClear) != 1 {
		t.Errorf("expected 1 synthetic clear on disconnect, got %d", pub.countKind(EphemeralEventClear))
	}
}
