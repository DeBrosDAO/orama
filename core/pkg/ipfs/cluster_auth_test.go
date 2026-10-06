package ipfs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"
)

// Requests to the cluster REST API carry the credentials install configured;
// the same client's requests to Kubo carry nothing.
func TestClient_authenticatesToTheClusterAPIOnly(t *testing.T) {
	password, err := ClusterRESTPassword("secret\n")
	if err != nil {
		t.Fatal(err)
	}
	cluster := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != ClusterRESTUser || pass != password {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer cluster.Close()
	var kuboAuth string
	kubo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		kuboAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer kubo.Close()

	c, err := NewClient(Config{ClusterAPIURL: cluster.URL, IPFSAPIURL: kubo.URL, ClusterAPIPassword: password}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Health(context.Background()); err != nil {
		t.Fatalf("the cluster API refused the client's credentials: %v", err)
	}
	resp, err := c.httpClient.Post(kubo.URL+"/api/v0/id", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if kuboAuth != "" {
		t.Errorf("the cluster's credentials were sent to Kubo: %q", kuboAuth)
	}
}

// Without the password the REST API refuses, and the client says so rather
// than reading an empty answer as healthy.
func TestClient_withoutCredentialsTheClusterAPIRefuses(t *testing.T) {
	cluster := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, _, ok := r.BasicAuth(); !ok {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer cluster.Close()
	c, err := NewClient(Config{ClusterAPIURL: cluster.URL}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Health(context.Background()); err == nil {
		t.Fatal("an unauthenticated health check succeeded")
	}
}

// The password is the cluster secret's, trimmed: two nodes whose copies differ
// by a newline derive the same one.
func TestClusterRESTPassword(t *testing.T) {
	a, err := ClusterRESTPassword("secret")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := ClusterRESTPassword("  secret\n")
	if a != b || len(a) != 64 {
		t.Errorf("passwords %q and %q", a, b)
	}
	if _, err := ClusterRESTPassword(""); err == nil {
		t.Error("an empty cluster secret derived a password")
	}
}

func TestNewClient_refusesAClusterURLWithNoHost(t *testing.T) {
	if _, err := NewClient(Config{ClusterAPIURL: "not a url", ClusterAPIPassword: "x"}, zap.NewNop()); err == nil {
		t.Fatal("credentials were configured for a URL with no host")
	}
}
