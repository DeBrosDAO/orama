package ipfs

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
)

const storedCID = "QmfYzxZHqYpmy29rVWqs6f4igzYngACaxSxPWdf7FspuDV"

// fakeNode is a Kubo API and a cluster API on one test server.
type fakeNode struct {
	heldLocally  bool
	inPinset     bool
	pinsetStalls bool
	networked    atomic.Int32
	pinsetAsked  atomic.Int32
}

func (n *fakeNode) serve(t *testing.T) (*Client, string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/allocations/"):
			n.pinsetAsked.Add(1)
			if n.pinsetStalls {
				<-r.Context().Done()
				return
			}
			if n.inPinset {
				_, _ = io.WriteString(w, `{}`)
				return
			}
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"code":404,"message":"pin is not part of the pinset"}`)
		case r.URL.Query().Get("offline") == "true":
			if n.heldLocally {
				_, _ = io.WriteString(w, "local bytes")
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, kuboOfflineMissBody)
		default:
			n.networked.Add(1)
			_, _ = io.WriteString(w, "peer bytes")
		}
	}))
	t.Cleanup(srv.Close)
	c, err := NewClient(Config{ClusterAPIURL: srv.URL}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	return c, srv.URL
}

func read(t *testing.T, r io.ReadCloser) string {
	t.Helper()
	defer r.Close()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestGetStored_localHitNeverAsksTheCluster(t *testing.T) {
	n := &fakeNode{heldLocally: true}
	c, url := n.serve(t)
	r, err := c.GetStored(context.Background(), storedCID, url)
	if err != nil || read(t, r) != "local bytes" || n.pinsetAsked.Load() != 0 {
		t.Fatalf("err=%v pinset asked %d times; want the local bytes with no pinset lookup", err, n.pinsetAsked.Load())
	}
}

func TestGetStored_goneIsErrNotInPinsetWithoutANetworkSearch(t *testing.T) {
	n := &fakeNode{}
	c, url := n.serve(t)
	_, err := c.GetStored(context.Background(), storedCID, url)
	if !errors.Is(err, ErrNotInPinset) || n.networked.Load() != 0 {
		t.Fatalf("err=%v networked=%d; want ErrNotInPinset and no network search", err, n.networked.Load())
	}
}

func TestGetStored_pinnedElsewhereIsFetchedFromPeers(t *testing.T) {
	n := &fakeNode{inPinset: true}
	c, url := n.serve(t)
	r, err := c.GetStored(context.Background(), storedCID, url)
	if err != nil || read(t, r) != "peer bytes" {
		t.Fatalf("err=%v; want the peer's bytes", err)
	}
}

func TestGetStored_aStalledPinsetIsBounded(t *testing.T) {
	n := &fakeNode{pinsetStalls: true}
	c, url := n.serve(t)
	start := time.Now()
	_, err := c.GetStored(context.Background(), storedCID, url)
	if !errors.Is(err, ErrPinsetUnavailable) {
		t.Fatalf("err=%v, want ErrPinsetUnavailable", err)
	}
	if took := time.Since(start); took > pinsetLookupTimeout+2*time.Second {
		t.Errorf("a stalled pinset held the read %s", took)
	}
}

// Kubo streams `cat`: a failure partway through arrives in the X-Stream-Error
// trailer after a 200 and some bytes. It must not read as a complete object.
func TestCatOnce_streamErrorTrailerIsAnError(t *testing.T) {
	for _, tc := range []struct {
		name     string
		offline  bool
		trailer  string
		wantMiss bool
	}{
		{"networked failure mid-stream", false, "context deadline exceeded", false},
		{"offline miss partway through the DAG", true, "block was not found locally (offline): ipld: could not find Qm", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Trailer", "X-Stream-Error")
				w.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(w, "partial")
				w.Header().Set("X-Stream-Error", tc.trailer)
			}))
			defer srv.Close()
			c, err := NewClient(Config{ClusterAPIURL: srv.URL}, zap.NewNop())
			if err != nil {
				t.Fatal(err)
			}
			body, err := c.catOnce(context.Background(), srv.URL, "Qm", tc.offline, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer body.Close()
			_, err = io.ReadAll(body)
			if err == nil {
				t.Fatal("a stream Kubo reported as failed read as complete")
			}
			if isContentNotFound(err) != tc.wantMiss {
				t.Errorf("err=%v, content-not-found=%v, want %v", err, isContentNotFound(err), tc.wantMiss)
			}
		})
	}
}

// When the caller's own deadline ends the pinset lookup, the answer is a
// timeout (retry later), not "the cluster could not be reached".
func TestGetStored_callerDeadlineDuringLookupIsATimeout(t *testing.T) {
	n := &fakeNode{pinsetStalls: true}
	c, url := n.serve(t)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := c.GetStored(ctx, storedCID, url)
	if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrPinsetUnavailable) {
		t.Fatalf("err=%v; want DeadlineExceeded, not ErrPinsetUnavailable", err)
	}
}
