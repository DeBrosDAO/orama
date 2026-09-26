package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/ipfs"
)

// The network status reads the cluster's REST API, which now requires the
// password; without sending it the read would come back 401 and the status
// would silently lose the cluster's peer id.
func TestQueryIPFSClusterPeerInfo_sendsTheRESTCredentials(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != ipfs.ClusterRESTUser || pass != "pw" || r.URL.Path != "/id" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"id":        "12D3KooWcluster",
			"addresses": []string{"/ip4/127.0.0.1/tcp/10114", "/ip4/10.0.0.3/tcp/10114"},
		})
	}))
	defer srv.Close()

	info := queryIPFSClusterPeerInfo(srv.URL, "pw")
	if info == nil || info.PeerID != "12D3KooWcluster" {
		t.Fatalf("info = %+v", info)
	}
	if len(info.Addresses) != 1 || info.Addresses[0] != "/ip4/10.0.0.3/tcp/10114" {
		t.Errorf("addresses = %v, want only the routable one", info.Addresses)
	}
	if queryIPFSClusterPeerInfo(srv.URL, "wrong") != nil {
		t.Error("a refused read was reported as peer info")
	}
}
