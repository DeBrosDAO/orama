package pubsub

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p"
	ps "github.com/libp2p/go-libp2p-pubsub"
	"go.uber.org/zap"
)

func TestHTTPAPI_publishSubscribe(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	gs, err := ps.NewGossipSub(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(gs, "", zap.NewNop())
	defer mgr.Close()

	srv := httptest.NewServer(Handler(mgr, zap.NewNop()))
	defer srv.Close()

	client := NewHTTPClient(srv.URL, "ns-a", zap.NewNop())
	defer client.Close()

	got := make(chan []byte, 1)
	if err := client.Subscribe(ctx, "chat", func(_ string, data []byte) error {
		got <- data
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(3 * time.Second)
	for {
		if err := client.Publish(ctx, "chat", []byte("hello")); err != nil {
			t.Fatal(err)
		}
		select {
		case msg := <-got:
			if string(msg) != "hello" {
				t.Fatalf("got %q, want hello", msg)
			}
			return
		case <-time.After(100 * time.Millisecond):
			select {
			case <-deadline:
				t.Fatal("timed out waiting for pubsub message")
			default:
			}
		}
	}
}
