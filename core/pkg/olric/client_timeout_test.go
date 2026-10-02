package olric

import (
	"context"
	"net"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestClientConfig_boundsEveryRoundTrip(t *testing.T) {
	for name, in := range map[string]time.Duration{
		"zero":           0,
		"negative":       -time.Second,
		"configured 30s": 30 * time.Second,
	} {
		cfg := clientConfig(in)
		if cfg.ReadTimeout != OperationTimeout || cfg.WriteTimeout != OperationTimeout {
			t.Errorf("%s: read/write timeout = %s/%s, want %s", name, cfg.ReadTimeout, cfg.WriteTimeout, OperationTimeout)
		}
		if cfg.MaxRetries != -1 {
			t.Errorf("%s: MaxRetries = %d, want -1 (a retry doubles the wait on an unreachable member)", name, cfg.MaxRetries)
		}
	}
}

func TestClientConfig_shorterTimeoutKept(t *testing.T) {
	if got := clientConfig(2 * time.Second).ReadTimeout; got != 2*time.Second {
		t.Fatalf("read timeout = %s, want the configured 2s", got)
	}
}

// A member that accepts the connection and never answers (a frozen process)
// must fail a call within the I/O deadline whatever context it carries: the
// client does not apply the context to the socket.
func TestClient_silentMemberFailsWithinTheDeadline(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close()
		}
	}()

	const deadline = 500 * time.Millisecond
	started := time.Now()
	c, err := NewClient(Config{Servers: []string{ln.Addr().String()}, Timeout: deadline}, zap.NewNop())
	if err == nil {
		dm, derr := c.GetClient().NewDMap("d")
		if derr == nil {
			err = dm.Put(context.Background(), "k", "v")
		} else {
			err = derr
		}
	}
	if err == nil {
		t.Fatal("a call to a member that never answers succeeded")
	}
	if elapsed := time.Since(started); elapsed > 5*deadline {
		t.Fatalf("the call took %s against a %s deadline", elapsed, deadline)
	}
}
