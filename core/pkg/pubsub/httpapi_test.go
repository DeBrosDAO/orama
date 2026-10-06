package pubsub

import (
	"context"
	"net"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p"
	ps "github.com/libp2p/go-libp2p-pubsub"
	"go.uber.org/zap"
)

// startAPI serves the pubsub HTTP API of a fresh manager on a unix socket and
// returns the socket path and the manager.
func startAPI(t *testing.T) (string, *Manager) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	gs, err := ps.NewGossipSub(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(gs, "", zap.NewNop())
	t.Cleanup(func() { _ = mgr.Close() })

	sock := shortSocketPath(t)
	ln, err := ListenSocket(sock, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: Handler(mgr, zap.NewNop())}
	go srv.Serve(ln)
	t.Cleanup(func() { _ = srv.Close() })
	return sock, mgr
}

func TestHTTPAPI_publishSubscribe(t *testing.T) {
	ctx := context.Background()
	sock, _ := startAPI(t)

	client := NewHTTPClient(sock, "ns-a", zap.NewNop())
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

// topicsEventually polls ListTopics until it equals want; the subscription a
// Subscribe opens reaches the service asynchronously.
func topicsEventually(t *testing.T, c *HTTPClient, ctx context.Context, want []string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		got, err := c.ListTopics(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if slices.Equal(got, want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("topics = %v, want %v", got, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestHTTPClient_ListTopics_listsOnlyTheNamespacesSubscribedTopics(t *testing.T) {
	ctx := context.Background()
	sock, _ := startAPI(t)
	a := NewHTTPClient(sock, "ns-a", zap.NewNop())
	defer a.Close()
	b := NewHTTPClient(sock, "ns-b", zap.NewNop())
	defer b.Close()
	noop := func(string, []byte) error { return nil }

	if err := a.Subscribe(ctx, "zeta", noop); err != nil {
		t.Fatal(err)
	}
	if err := a.Subscribe(ctx, "alpha", noop); err != nil {
		t.Fatal(err)
	}
	if err := b.Subscribe(ctx, "other", noop); err != nil {
		t.Fatal(err)
	}

	topicsEventually(t, a, ctx, []string{"alpha", "zeta"})
	topicsEventually(t, b, ctx, []string{"other"})
}

func TestHTTPClient_ListTopics_emptyNamespaceIsAnEmptyList(t *testing.T) {
	sock, _ := startAPI(t)
	c := NewHTTPClient(sock, "nobody", zap.NewNop())
	defer c.Close()
	got, err := c.ListTopics(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("topics = %v, want none", got)
	}
}

func TestHTTPClient_ListTopics_honoursTheNamespaceOverride(t *testing.T) {
	sock, _ := startAPI(t)
	c := NewHTTPClient(sock, "default-ns", zap.NewNop())
	defer c.Close()
	override := WithNamespace(context.Background(), "tenant")
	if err := c.Subscribe(override, "t1", func(string, []byte) error { return nil }); err != nil {
		t.Fatal(err)
	}
	topicsEventually(t, c, override, []string{"t1"})
	got, err := c.ListTopics(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("default namespace topics = %v, want none", got)
	}
}

func TestHTTPClient_ListTopics_serviceErrorIsReturned(t *testing.T) {
	sock := shortSocketPath(t)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})}
	go srv.Serve(ln)
	defer srv.Close()
	c := NewHTTPClient(sock, "ns", zap.NewNop())
	defer c.Close()
	if _, err := c.ListTopics(context.Background()); err == nil {
		t.Fatal("a 500 from the service must be an error, not an empty list")
	}
}

func TestHandler_topicsRequiresNamespaceAndGet(t *testing.T) {
	sock, _ := startAPI(t)
	c := NewHTTPClient(sock, "ns", zap.NewNop())
	defer c.Close()
	for _, tc := range []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/topics", http.StatusBadRequest},
		{http.MethodPost, "/topics?namespace=ns", http.StatusMethodNotAllowed},
	} {
		req, _ := http.NewRequest(tc.method, c.baseURL+tc.path, nil)
		resp, err := c.http.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != tc.want {
			t.Errorf("%s %s = %d, want %d", tc.method, tc.path, resp.StatusCode, tc.want)
		}
	}
}
