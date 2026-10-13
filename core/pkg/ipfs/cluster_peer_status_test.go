package ipfs

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/auth"
	"go.uber.org/zap"
)

const peerID = "12D3KooWDiscoveredPeer"

// A peer's network status answers another node only with the coordination
// MAC; discovery signs every request it makes.
func TestFetchPeerNetworkStatus_signsTheRequest(t *testing.T) {
	key, err := auth.CoordinationKey("discovery-secret")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/network/status" || !auth.VerifyCoordination(key, r, time.Now(), peerID) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	resp, err := fetchPeerNetworkStatus(srv.Client(), key, srv.URL, peerID)
	if err != nil {
		t.Fatalf("a signed request was refused: %v", err)
	}
	resp.Body.Close()

	if _, err := fetchPeerNetworkStatus(srv.Client(), key, srv.URL, "12D3KooWAnotherNode"); err == nil {
		t.Error("a request signed for another peer was read as this peer's status")
	}

	other, _ := auth.CoordinationKey("another-cluster")
	if _, err := fetchPeerNetworkStatus(srv.Client(), other, srv.URL, peerID); err == nil {
		t.Error("a refused request was read as a status")
	}
}

// Two peer ids can be known for one overlay address; the request must be
// signed for the registered node's id, never another.
func TestDiscoverFrom_signsForTheRegisteredNodeId(t *testing.T) {
	key, err := auth.CoordinationKey("discovery-secret")
	if err != nil {
		t.Fatal(err)
	}
	var verified, refused int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth.VerifyCoordination(key, r, time.Now(), peerID) {
			verified++
		} else {
			refused++
		}
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	cm := &ClusterConfigManager{secret: "discovery-secret", logger: zap.NewNop()}
	targets := []PeerTarget{{ID: peerID, IP: "10.0.0.2"}}
	if err := cm.discoverFrom(targets, func(string) string { return srv.URL }); err != nil {
		t.Fatal(err)
	}
	if verified != 1 || refused != 0 {
		t.Fatalf("verified=%d refused=%d, want the stamp signed for the registered id", verified, refused)
	}
}

func TestDiscoverClusterPeers_registryFailureIsReturned(t *testing.T) {
	cm := &ClusterConfigManager{secret: "s", logger: zap.NewNop()}
	err := cm.DiscoverClusterPeers(context.Background(), "self", func(context.Context) ([]PeerTarget, error) {
		return nil, errors.New("rqlite down")
	})
	if err == nil {
		t.Fatal("a registry failure was swallowed")
	}
}

func TestDiscoverClusterPeers_skipsSelf(t *testing.T) {
	var asked int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { asked++ }))
	defer srv.Close()
	cm := &ClusterConfigManager{secret: "s", logger: zap.NewNop()}
	err := cm.DiscoverClusterPeers(context.Background(), "self", func(context.Context) ([]PeerTarget, error) {
		return []PeerTarget{{ID: "self", IP: "10.0.0.1"}}, nil
	})
	if err != nil || asked != 0 {
		t.Fatalf("err=%v asked=%d", err, asked)
	}
}
