package olric

import (
	"testing"

	"go.uber.org/zap"

	"github.com/DeBrosOfficial/network/pkg/olric/olrictest"
)

func TestCurrent_followsSetAndDrop(t *testing.T) {
	var c *Current
	if c.Underlying() != nil {
		t.Fatal("a nil Current held a client")
	}
	c.Set(nil) // must not panic

	c = &Current{}
	if c.Underlying() != nil {
		t.Fatal("a fresh Current held a client")
	}
	srv := olrictest.Start(t)
	client, err := NewClient(Config{Servers: []string{srv.Addr}}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	c.Set(client)
	if c.Underlying() != client.UnderlyingClient() {
		t.Fatal("Underlying is not the client that was set")
	}
	c.Set(nil)
	if c.Underlying() != nil {
		t.Fatal("a dropped client is still held (and must be an untyped nil)")
	}
}
