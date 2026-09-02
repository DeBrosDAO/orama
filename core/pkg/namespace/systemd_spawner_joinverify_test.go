package namespace

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/zap"
)

// Bugboard #275. rqlited joins whatever answers at its -join address; nothing in
// the protocol asserts the cluster belongs to the right namespace. On devnet a port
// collision put an orphaned `rootwallet` namespace's rqlited on the port anchat-v2
// had been allocated, and anchat-v2's node joined THAT raft group as a Voter — it
// then served rootwallet's database (identical row counts on a namespace minutes
// old) and pushed rootwallet's quorum from 2 to 3 underneath it.
//
// The join target's /status reports the data directory it serves, rooted at
// .../namespaces/<namespace>/rqlite/<nodeID>. That is an unforgeable statement of
// which namespace the cluster belongs to.

// statusServer stands in for an rqlited reporting the directory it serves.
func statusServer(t *testing.T, dir string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/status" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"store":{"dir":"` + dir + `"}}`))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// The exact incident: the target is serving a DIFFERENT namespace.
func TestVerifyJoinTarget_refusesForeignNamespace(t *testing.T) {
	url := statusServer(t, "/opt/orama/.orama/data/namespaces/rootwallet/rqlite/12D3KooWC3ucq")
	s := NewSystemdSpawner(t.TempDir(), "", zap.NewNop())

	err := s.verifyJoinTarget(context.Background(), "anchat-v2", url)
	if err == nil {
		t.Fatal("verifyJoinTarget accepted a foreign namespace's raft group — this is the cross-namespace data exposure")
	}
	if !strings.Contains(err.Error(), "rootwallet") {
		t.Errorf("error should name the namespace actually being served: %v", err)
	}
	if !strings.Contains(err.Error(), "anchat-v2") {
		t.Errorf("error should name the namespace being provisioned: %v", err)
	}
}

// The normal case: same namespace, join proceeds.
func TestVerifyJoinTarget_acceptsOwnNamespace(t *testing.T) {
	url := statusServer(t, "/opt/orama/.orama/data/namespaces/anchat-v2/rqlite/12D3KooWGpb1p")
	s := NewSystemdSpawner(t.TempDir(), "", zap.NewNop())

	if err := s.verifyJoinTarget(context.Background(), "anchat-v2", url); err != nil {
		t.Errorf("verifyJoinTarget rejected the namespace's own cluster: %v", err)
	}
}

// A namespace whose name is a prefix of another must not be accepted — the check
// is on a full path segment, not a substring.
func TestVerifyJoinTarget_rejectsPrefixNamespaceCollision(t *testing.T) {
	url := statusServer(t, "/opt/orama/.orama/data/namespaces/anchat-v2-staging/rqlite/node")
	s := NewSystemdSpawner(t.TempDir(), "", zap.NewNop())

	if err := s.verifyJoinTarget(context.Background(), "anchat-v2", url); err == nil {
		t.Error("verifyJoinTarget accepted anchat-v2-staging for anchat-v2 — namespace matching must be on a whole path segment")
	}
}

// The leader joins nothing, so an empty URL must be a no-op rather than an error.
func TestVerifyJoinTarget_emptyURLSkipsCheck(t *testing.T) {
	s := NewSystemdSpawner(t.TempDir(), "", zap.NewNop())

	if err := s.verifyJoinTarget(context.Background(), "anchat-v2", ""); err != nil {
		t.Errorf("empty verify URL should skip the check (the leader joins nothing): %v", err)
	}
}

// An unreachable target must fail loudly rather than being treated as verified —
// otherwise the check could be bypassed by the target simply being down.
func TestVerifyJoinTarget_unreachableTargetFails(t *testing.T) {
	s := NewSystemdSpawner(t.TempDir(), "", zap.NewNop())

	// Port 1 on loopback: nothing listens there.
	if err := s.verifyJoinTarget(context.Background(), "anchat-v2", "http://127.0.0.1:1"); err == nil {
		t.Error("verifyJoinTarget succeeded against an unreachable target — an unverifiable join must not proceed")
	}
}

// A target that answers with something other than a status document must fail
// closed too.
func TestVerifyJoinTarget_malformedStatusFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	t.Cleanup(srv.Close)
	s := NewSystemdSpawner(t.TempDir(), "", zap.NewNop())

	if err := s.verifyJoinTarget(context.Background(), "anchat-v2", srv.URL); err == nil {
		t.Error("verifyJoinTarget succeeded on an unparseable status response")
	}
}
