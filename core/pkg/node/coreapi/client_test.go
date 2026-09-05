package coreapi

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/auth"
	"github.com/DeBrosOfficial/network/pkg/nodeapi"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

// nodeIdentity generates a libp2p identity and the peer id that carries its
// public key. Enrolment is checked against the key inside the id, so a made-up
// peer id could never enrol.
func nodeIdentity(t *testing.T) (string, crypto.PrivKey) {
	t.Helper()
	priv, pub, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateEd25519Key: %v", err)
	}
	id, err := peer.IDFromPublicKey(pub)
	if err != nil {
		t.Fatalf("IDFromPublicKey: %v", err)
	}
	return id.String(), priv
}

// verifyAnything accepts any well-formed stamp. The tests that use it are about
// what the client sends and how it reports an answer, not about who may sign;
// the tests that are about that resolve the real way.
var verifyAnything = auth.NodeVerifierFor(func(nodeID string) (auth.NodeStampVerifier, error) {
	return anySignature{}, nil
})

type anySignature struct{}

func (anySignature) Verify(_, sig []byte) bool { return len(sig) > 0 }

// verifying is a gateway that checks the stamp the way the real handler does,
// so these tests prove the client and the handler agree about what is signed
// rather than proving the client agrees with itself.
func verifying(t *testing.T, answer func(w http.ResponseWriter, nodeID string, body []byte)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "unreadable", http.StatusBadRequest)
			return
		}
		nodeID, _, ok := auth.VerifyNodeAPI(verifyAnything, r, body, time.Now())
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		answer(w, nodeID, body)
	}))
}

func client(t *testing.T, baseURL string) *Client {
	t.Helper()
	own, err := auth.NewNodeKeyPair()
	if err != nil {
		t.Fatalf("NewNodeKeyPair: %v", err)
	}
	nodeID, identity := nodeIdentity(t)
	c, err := New(baseURL, nodeID, own, identity)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

// A registration the client sends is one the gateway accepts, names this node,
// and carries the fields as sent.
func TestRegister_isAcceptedAndNamesThisNode(t *testing.T) {
	var seenNode string
	var seen nodeapi.RegisterRequest

	srv := verifying(t, func(w http.ResponseWriter, nodeID string, body []byte) {
		seenNode = nodeID
		if err := json.Unmarshal(body, &seen); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	defer srv.Close()

	sent := nodeapi.RegisterRequest{
		IPAddress:  "203.0.113.7",
		InternalIP: "10.0.0.7",
		Region:     "local",
		SSHUser:    "orama",
	}
	if err := client(t, srv.URL).Register(context.Background(), sent); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if seenNode == "" {
		t.Error("the gateway could not read which node sent the registration")
	}
	if seen != sent {
		t.Errorf("the gateway received %+v, want %+v", seen, sent)
	}
}

// The client signs what it sends. A stamp over a different body would be
// refused by the real handler, and this is what catches a future change that
// signs before the body is final.
func TestRegister_theStampCoversTheBodyThatIsSent(t *testing.T) {
	srv := verifying(t, func(w http.ResponseWriter, _ string, _ []byte) {
		w.WriteHeader(http.StatusNoContent)
	})
	defer srv.Close()

	if err := client(t, srv.URL).Register(context.Background(), nodeapi.RegisterRequest{
		IPAddress:  "203.0.113.7",
		InternalIP: "10.0.0.7",
		Region:     "local",
	}); err != nil {
		t.Fatalf("a request the client signed was refused by a gateway checking the stamp: %v", err)
	}
}

// The heartbeat reports what the gateway said about the row.
func TestHeartbeat_reportsWhetherTheRowExists(t *testing.T) {
	for _, registered := range []bool{true, false} {
		srv := verifying(t, func(w http.ResponseWriter, _ string, _ []byte) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(nodeapi.HeartbeatResponse{Registered: registered})
		})

		got, err := client(t, srv.URL).Heartbeat(context.Background())
		srv.Close()
		if err != nil {
			t.Fatalf("Heartbeat: %v", err)
		}
		if got != registered {
			t.Errorf("Heartbeat = %v, want %v", got, registered)
		}
	}
}

// A refusal is surfaced with what the gateway said and where it was called, so
// the log line says what to fix rather than "request failed".
func TestPost_aRefusalSaysWhatAndWhere(t *testing.T) {
	// Not a 401: that one means "the cluster does not have this node's key" and
	// is handled by recording it again, which the tests below cover.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "internal_ip does not match this node's overlay allocation", http.StatusBadRequest)
	}))
	defer srv.Close()

	err := client(t, srv.URL).Register(context.Background(), nodeapi.RegisterRequest{})
	if err == nil {
		t.Fatal("a refused registration was reported as success")
	}
	for _, want := range []string{"/v1/internal/node/register", srv.URL, "400", "overlay allocation"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not mention %q: %v", want, err)
		}
	}
}

// An answer that is not the shape the gateway promises is an error, not a
// silent false.
func TestHeartbeat_anUnreadableAnswerIsReported(t *testing.T) {
	srv := verifying(t, func(w http.ResponseWriter, _ string, _ []byte) {
		_, _ = w.Write([]byte("not json"))
	})
	defer srv.Close()

	if _, err := client(t, srv.URL).Heartbeat(context.Background()); err == nil {
		t.Error("an unreadable answer was reported as a heartbeat result")
	}
}

// A gateway that cannot be reached at all names the address it tried, because
// the fix is almost always that the local gateway is not up.
func TestPost_anUnreachableGatewayNamesTheAddress(t *testing.T) {
	srv := verifying(t, func(w http.ResponseWriter, _ string, _ []byte) {})
	url := srv.URL
	srv.Close()

	err := client(t, url).Register(context.Background(), nodeapi.RegisterRequest{})
	if err == nil {
		t.Fatal("a call to a gateway that is not there succeeded")
	}
	if !strings.Contains(err.Error(), url) {
		t.Errorf("the error does not name the address it tried: %v", err)
	}
}

// A cancelled context stops the call rather than blocking the heartbeat loop.
func TestPost_respectsACancelledContext(t *testing.T) {
	srv := verifying(t, func(w http.ResponseWriter, _ string, _ []byte) {
		w.WriteHeader(http.StatusNoContent)
	})
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := client(t, srv.URL).Register(ctx, nodeapi.RegisterRequest{}); err == nil {
		t.Error("a call with a cancelled context succeeded")
	}
}

// A client that cannot sign is refused at construction rather than one request
// at a time, in a warning log.
func TestNew_refusesAClientThatCouldNotSign(t *testing.T) {
	own, err := auth.NewNodeKeyPair()
	if err != nil {
		t.Fatalf("NewNodeKeyPair: %v", err)
	}
	nodeID, identity := nodeIdentity(t)
	cases := map[string]struct {
		url, node string
		key       *auth.NodeKeyPair
		identity  crypto.PrivKey
	}{
		"no gateway address": {"", nodeID, own, identity},
		"no node id":         {"http://127.0.0.1:1", "  ", own, identity},
		"no key of its own":  {"http://127.0.0.1:1", nodeID, nil, identity},
		"no libp2p identity": {"http://127.0.0.1:1", nodeID, own, nil},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := New(c.url, c.node, c.key, c.identity); err == nil {
				t.Errorf("a client with %s was built", name)
			}
		})
	}
}

// A trailing slash on the gateway address must not produce a double slash in
// the path, which is a different route and would 404.
func TestNew_toleratesATrailingSlashOnTheAddress(t *testing.T) {
	var path string
	srv := verifying(t, func(w http.ResponseWriter, _ string, _ []byte) {
		w.WriteHeader(http.StatusNoContent)
	})
	defer srv.Close()

	inner := srv.Config.Handler
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		inner.ServeHTTP(w, r)
	})

	own, err := auth.NewNodeKeyPair()
	if err != nil {
		t.Fatalf("NewNodeKeyPair: %v", err)
	}
	nodeID, identity := nodeIdentity(t)
	c, err := New(srv.URL+"/", nodeID, own, identity)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := c.Register(context.Background(), nodeapi.RegisterRequest{}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if path != "/v1/internal/node/register" {
		t.Errorf("called %q, want /v1/internal/node/register", path)
	}
}

// Enrolment is signed with this node's libp2p identity and everything else
// with the key it enrolled — including on the second and every later start,
// which is the path that would brick the fleet if it were wrong.
func TestEnrolKey_isSignedByTheIdentityAndEverythingElseByTheKey(t *testing.T) {
	own, err := auth.NewNodeKeyPair()
	if err != nil {
		t.Fatalf("NewNodeKeyPair: %v", err)
	}
	pub, err := auth.ParseNodePublicKey(own.PublicKey())
	if err != nil {
		t.Fatalf("ParseNodePublicKey: %v", err)
	}
	nodeID, identity := nodeIdentity(t)

	// The gateway applies the real rule: enrolment against the key inside the
	// peer id, everything else against the key the node enrolled.
	var enrolments, heartbeats int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "unreadable", http.StatusBadRequest)
			return
		}
		verifier := auth.NodeVerifierFor(func(string) (auth.NodeStampVerifier, error) { return pub, nil })
		if r.URL.Path == nodeapi.PathEnrolKey {
			verifier = auth.NodeIdentityVerifier
		}
		if _, _, ok := auth.VerifyNodeAPI(verifier, r, body, time.Now()); !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case nodeapi.PathEnrolKey:
			enrolments++
			_ = json.NewEncoder(w).Encode(nodeapi.EnrolKeyResponse{Recorded: enrolments == 1})
		default:
			heartbeats++
			_ = json.NewEncoder(w).Encode(nodeapi.HeartbeatResponse{Registered: true})
		}
	}))
	defer srv.Close()

	c, err := New(srv.URL, nodeID, own, identity)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := c.EnrolKey(context.Background()); err != nil {
		t.Fatalf("EnrolKey: %v", err)
	}
	if _, err := c.Heartbeat(context.Background()); err != nil {
		t.Fatalf("the client did not sign with its enrolled key: %v", err)
	}

	// The second start: a fresh client with the same key against a cluster that
	// already has the row. This is what every restart does, and signing the
	// enrolment with anything the cluster cannot check would refuse it.
	second, err := New(srv.URL, nodeID, own, identity)
	if err != nil {
		t.Fatalf("New again: %v", err)
	}
	if err := second.EnrolKey(context.Background()); err != nil {
		t.Fatalf("a node could not re-assert its key on a second start: %v", err)
	}
	if _, err := second.Heartbeat(context.Background()); err != nil {
		t.Fatalf("a restarted node could not heartbeat: %v", err)
	}
	if enrolments != 2 || heartbeats != 2 {
		t.Errorf("enrolments=%d heartbeats=%d, want 2 and 2", enrolments, heartbeats)
	}
}

// A row can disappear under a running node — a registry restored from an older
// backup, an operator clearing it. The node records its key again and carries
// on, rather than being refused every 30 seconds until somebody restarts it.
func TestPost_reEnrolsWhenTheClusterNoLongerHasTheKey(t *testing.T) {
	own, err := auth.NewNodeKeyPair()
	if err != nil {
		t.Fatalf("NewNodeKeyPair: %v", err)
	}
	pub, err := auth.ParseNodePublicKey(own.PublicKey())
	if err != nil {
		t.Fatalf("ParseNodePublicKey: %v", err)
	}
	nodeID, identity := nodeIdentity(t)

	recorded := false
	var enrolments, registrations int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.URL.Path == nodeapi.PathEnrolKey {
			if _, _, ok := auth.VerifyNodeAPI(auth.NodeIdentityVerifier, r, body, time.Now()); !ok {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			enrolments++
			recorded = true
			_ = json.NewEncoder(w).Encode(nodeapi.EnrolKeyResponse{Recorded: true})
			return
		}
		// The cluster has forgotten this node until it enrols again.
		verifier := auth.NodeVerifierFor(func(string) (auth.NodeStampVerifier, error) {
			if recorded {
				return pub, nil
			}
			return nil, nil
		})
		if _, _, ok := auth.VerifyNodeAPI(verifier, r, body, time.Now()); !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		registrations++
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c, err := New(srv.URL, nodeID, own, identity)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// No enrolment yet, so the first attempt is refused — and the client
	// records its key and retries rather than giving up.
	if err := c.Register(context.Background(), nodeapi.RegisterRequest{}); err != nil {
		t.Fatalf("the client did not recover from the cluster forgetting its key: %v", err)
	}
	if enrolments != 1 || registrations != 1 {
		t.Errorf("enrolments=%d registrations=%d, want 1 and 1", enrolments, registrations)
	}
}

// The retry happens once. A cluster refusing for some other reason surfaces
// that refusal rather than being asked forever.
func TestPost_reEnrolsAtMostOnce(t *testing.T) {
	var enrolments, attempts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == nodeapi.PathEnrolKey {
			enrolments++
			_ = json.NewEncoder(w).Encode(nodeapi.EnrolKeyResponse{Recorded: true})
			return
		}
		attempts++
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()

	if err := client(t, srv.URL).Register(context.Background(), nodeapi.RegisterRequest{}); err == nil {
		t.Fatal("a request refused twice was reported as success")
	}
	if attempts != 2 || enrolments != 1 {
		t.Errorf("attempts=%d enrolments=%d, want 2 and 1", attempts, enrolments)
	}
}

// A refused enrolment is reported rather than leaving the client signing with
// the shared credential and looking like it worked.
func TestEnrolKey_aRefusalIsReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "this node already has a different key on record", http.StatusBadRequest)
	}))
	defer srv.Close()

	err := client(t, srv.URL).EnrolKey(context.Background())
	if err == nil {
		t.Fatal("a refused enrolment was reported as success")
	}
	if !strings.Contains(err.Error(), "already has a different key") {
		t.Errorf("the error does not say what the gateway said: %v", err)
	}
}
