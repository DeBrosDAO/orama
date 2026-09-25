package ipfs

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.uber.org/zap"
)

// kuboOfflineMissBody is Kubo 0.38.2's exact answer to an offline `cat` of a
// block it does not hold, captured from a live daemon: HTTP 500, not 404.
const kuboOfflineMissBody = `{"Message":"block was not found locally (offline): ipld: could not find QmfYzxZHqYpmy29rVWqs6f4igzYngACaxSxPWdf7FspuDV","Code":0,"Type":"error"}`

// bugboard #414: Get recognised only a 404 as "not held here", so on a node
// that did not hold an object it never fetched it from its peers.
func TestGet_kuboOfflineMissFallsThroughToTheNetwork(t *testing.T) {
	var networked bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("offline") == "true" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, kuboOfflineMissBody)
			return
		}
		networked = true
		_, _ = io.WriteString(w, "fetched from a peer")
	}))
	defer srv.Close()
	c, err := NewClient(Config{ClusterAPIURL: srv.URL}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}

	r, err := c.Get(context.Background(), "QmfYzxZHqYpmy29rVWqs6f4igzYngACaxSxPWdf7FspuDV", srv.URL)
	if err != nil {
		t.Fatalf("Get: %v; a local miss must fall through to a networked fetch", err)
	}
	defer r.Close()
	body, _ := io.ReadAll(r)
	if !networked || string(body) != "fetched from a peer" {
		t.Fatalf("networked=%v body=%q; want the networked attempt's content", networked, body)
	}
}

// A 500 that is not a local miss is a real failure of this node and is not
// retried over the network.
func TestGet_otherOfflineFailureIsReturned(t *testing.T) {
	var networked bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("offline") != "true" {
			networked = true
		}
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"Message":"repo is locked","Code":0,"Type":"error"}`)
	}))
	defer srv.Close()
	c, err := NewClient(Config{ClusterAPIURL: srv.URL}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get(context.Background(), "QmfYzxZHqYpmy29rVWqs6f4igzYngACaxSxPWdf7FspuDV", srv.URL); err == nil || networked {
		t.Fatalf("err=%v networked=%v; want the local failure returned without a networked attempt", err, networked)
	}
}

// The offline attempt's timeout bounds the wait for Kubo to answer, not the
// read: a large object this node holds must not be cut off mid-body.
func TestCatOnce_headerTimeoutDoesNotBoundTheBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		_, _ = io.WriteString(w, "first ")
		w.(http.Flusher).Flush()
		time.Sleep(300 * time.Millisecond)
		_, _ = io.WriteString(w, "second")
	}))
	defer srv.Close()
	c, err := NewClient(Config{ClusterAPIURL: srv.URL}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}

	r, err := c.catOnce(context.Background(), srv.URL, "Qm", true, 100*time.Millisecond)
	if err != nil {
		t.Fatalf("catOnce: %v", err)
	}
	defer r.Close()
	body, err := io.ReadAll(r)
	if err != nil || string(body) != "first second" {
		t.Fatalf("body = %q, err = %v; the header timeout cut off the body", body, err)
	}
}

func TestCatOnce_headerTimeoutStopsAWedgedDaemon(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)
	c, err := NewClient(Config{ClusterAPIURL: srv.URL}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	_, err = c.catOnce(context.Background(), srv.URL, "Qm", true, 100*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("catOnce against a daemon that never answers: %v, want a DeadlineExceeded (a TIMEOUT, not a 500)", err)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("the header timeout took %s to fire", took)
	}
}
