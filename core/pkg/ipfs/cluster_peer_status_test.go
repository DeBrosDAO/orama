package ipfs

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/auth"
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
