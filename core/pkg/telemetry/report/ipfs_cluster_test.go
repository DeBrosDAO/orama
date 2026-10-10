package report

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/ipfs"
)

// clusterGet asks the REST API where it listens, with its credentials. The
// report used to ask port 9094, where nothing listens, unauthenticated.
func TestClusterGet_authenticatesToTheLocalRESTAPI(t *testing.T) {
	var gotUser, gotPass string
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser, gotPass, _ = r.BasicAuth()
		if gotPass != "pw" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{"version":"1.1.2"}`))
	}))
	ln, err := net.Listen("tcp", constants.LocalIPFSClusterURL()[len("http://"):])
	if err != nil {
		t.Skipf("the cluster API port is in use on this machine: %v", err)
	}
	srv.Listener = ln
	srv.Start()
	defer srv.Close()

	body, err := clusterGet(context.Background(), "/id", "pw")
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `{"version":"1.1.2"}` || gotUser != ipfs.ClusterRESTUser {
		t.Errorf("body %s, user %q", body, gotUser)
	}
	if _, err := clusterGet(context.Background(), "/id", "wrong"); err == nil {
		t.Error("a refused request was read as an answer")
	}
}
