package rqlite

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// nodesAt serves /nodes with the given raft configuration.
func nodesAt(t *testing.T, body string) Endpoint {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/nodes" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	return Endpoint{Host: u.Hostname(), Port: port, Username: "orama", Password: "secret"}
}

func TestVerifyJoined_memberOfTheTargetsCluster(t *testing.T) {
	ep := nodesAt(t, `{"nodes":[
		{"id":"a","addr":"10.0.0.1:10101","voter":true,"leader":true},
		{"id":"b","addr":"10.0.0.2:10101","voter":true}]}`)
	if err := VerifyJoined(context.Background(), ep, "10.0.0.1:10101"); err != nil {
		t.Fatalf("VerifyJoined: %v", err)
	}
}

// Superman after its join was refused: raft ready, leader, and alone.
func TestVerifyJoined_aClusterOfItsOwn(t *testing.T) {
	ep := nodesAt(t, `{"nodes":[{"id":"b","addr":"10.0.0.2:10101","voter":true,"leader":true}]}`)
	err := VerifyJoined(context.Background(), ep, "10.0.0.1:10101")
	if err == nil {
		t.Fatal("a node leading a cluster of one passed as joined")
	}
	for _, want := range []string{"10.0.0.2:10101", "10.0.0.1:10101", "cluster of its own"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q: %v", want, err)
		}
	}
}

func TestVerifyJoined_unreadableConfiguration(t *testing.T) {
	ep := nodesAt(t, `not json`)
	if err := VerifyJoined(context.Background(), ep, "10.0.0.1:10101"); err == nil {
		t.Fatal("an unreadable /nodes passed as joined")
	}
}
