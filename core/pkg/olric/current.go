package olric

import (
	"sync"

	olriclib "github.com/olric-data/olric"
)

// Current holds the Olric client the gateway is connected to right now, which
// is none while the supervisor has dropped it and a new one once it reconnects.
// Consumers that outlive a connection (serverless host functions, the pub/sub
// dispatcher) read it on every operation through Underlying instead of
// capturing a client at startup: a captured client is dead after the first
// reconnect and stays dead until the gateway restarts.
//
// A nil *Current holds nothing. Safe for concurrent use.
type Current struct {
	mu     sync.RWMutex
	client *Client
}

// Set records the connected client, or nil when it was dropped.
func (c *Current) Set(client *Client) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.client = client
	c.mu.Unlock()
}

// Underlying is the connected cluster client, or nil when there is none. The
// nil is an untyped one, so callers can compare it with nil.
func (c *Current) Underlying() olriclib.Client {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.client == nil {
		return nil
	}
	return c.client.UnderlyingClient()
}
