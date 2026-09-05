package auth

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// testSigner and testVerifier are one node's key: the tests below are about
// what a stamp binds, not about whose key made it.
var testSigner, testVerifier = func() (NodeStampSigner, NodeVerifierFor) {
	key, err := NewNodeKeyPair()
	if err != nil {
		panic(err)
	}
	pub, err := ParseNodePublicKey(key.PublicKey())
	if err != nil {
		panic(err)
	}
	return key, func(string) (NodeStampVerifier, error) { return pub, nil }
}()

func signedRequest(t *testing.T, method, url, nodeID string, body []byte, now time.Time) *http.Request {
	t.Helper()
	r := httptest.NewRequest(method, url, bytes.NewReader(body))
	if err := SignNodeAPI(testSigner, r, nodeID, body, now); err != nil {
		t.Fatalf("SignNodeAPI: %v", err)
	}
	return r
}

// A stamped request names the node that stamped it, and the name is the one
// the handler acts on.
func TestNodeAPI_aStampNamesTheNodeThatMadeIt(t *testing.T) {
	now := time.Now()
	body := []byte(`{"ip_address":"1.2.3.4"}`)
	r := signedRequest(t, http.MethodPost, "/v1/internal/node/register", "node-a", body, now)

	nodeID, _, ok := VerifyNodeAPI(testVerifier, r, body, now)
	if !ok {
		t.Fatal("a request this node stamped was not accepted")
	}
	if nodeID != "node-a" {
		t.Errorf("verified node id = %q, want %q", nodeID, "node-a")
	}
}

// The claim these requests carry is in the body — an IP address, an operator
// wallet. A stamp that did not cover it could be lifted onto a body of the
// caller's choosing, which is the whole point of stamping them.
func TestNodeAPI_aStampDoesNotCoverADifferentBody(t *testing.T) {
	now := time.Now()
	signed := []byte(`{"ip_address":"1.2.3.4"}`)
	r := signedRequest(t, http.MethodPost, "/v1/internal/node/register", "node-a", signed, now)

	swapped := []byte(`{"ip_address":"9.9.9.9"}`)
	if _, _, ok := VerifyNodeAPI(testVerifier, r, swapped, now); ok {
		t.Error("a stamp over one body was accepted for another")
	}
}

// A stamp cannot be lifted onto a request about a different node — that is what
// stops a node registering, or reaping, somebody else's row.
func TestNodeAPI_aStampDoesNotCoverADifferentNode(t *testing.T) {
	now := time.Now()
	body := []byte(`{}`)
	r := signedRequest(t, http.MethodPost, "/v1/internal/node/heartbeat", "node-a", body, now)

	r.Header.Set(NodeIDHeader, "node-b")
	if _, _, ok := VerifyNodeAPI(testVerifier, r, body, now); ok {
		t.Error("a stamp naming one node was accepted as another")
	}
}

// Nor onto a different endpoint: registering and heartbeating do different
// things, and a stamp for one must not authorise the other.
func TestNodeAPI_aStampDoesNotCoverADifferentRequest(t *testing.T) {
	now := time.Now()
	body := []byte(`{}`)

	movedPath := signedRequest(t, http.MethodPost, "/v1/internal/node/heartbeat", "node-a", body, now)
	movedPath.URL.Path = "/v1/internal/node/register"
	if _, _, ok := VerifyNodeAPI(testVerifier, movedPath, body, now); ok {
		t.Error("a stamp for one path was accepted on another")
	}

	movedMethod := signedRequest(t, http.MethodPost, "/v1/internal/node/register", "node-a", body, now)
	movedMethod.Method = http.MethodDelete
	if _, _, ok := VerifyNodeAPI(testVerifier, movedMethod, body, now); ok {
		t.Error("a stamp for one method was accepted on another")
	}
}

// Replay is bounded in both directions. A future timestamp is as much a sign of
// a forged stamp as an old one.
func TestNodeAPI_aStampExpiresInBothDirections(t *testing.T) {
	body := []byte(`{}`)
	signedAt := time.Now()
	r := signedRequest(t, http.MethodPost, "/v1/internal/node/register", "node-a", body, signedAt)
	keys := testVerifier

	if _, _, ok := VerifyNodeAPI(keys, r, body, signedAt.Add(nodeAPIMaxSkew-time.Second)); !ok {
		t.Error("a stamp inside the window was refused")
	}
	if _, _, ok := VerifyNodeAPI(keys, r, body, signedAt.Add(nodeAPIMaxSkew+time.Second)); ok {
		t.Error("a stale stamp was accepted")
	}
	if _, _, ok := VerifyNodeAPI(keys, r, body, signedAt.Add(-nodeAPIMaxSkew-time.Second)); ok {
		t.Error("a stamp from the future was accepted")
	}
}

// A stamp made with one cluster's secret means nothing in another.
func TestNodeAPI_aStampFromAnotherClusterIsRefused(t *testing.T) {
	now := time.Now()
	body := []byte(`{}`)
	r := signedRequest(t, http.MethodPost, "/v1/internal/node/register", "node-a", body, now)

	if _, _, ok := VerifyNodeAPI(otherNodeVerifier(t), r, body, now); ok {
		t.Error("a stamp from another cluster was accepted")
	}
}

// The key is resolved by node id. That is the seam a per-node credential
// arrives through: a resolver that has no key for a node refuses it, and
// nothing else in the protocol changes.
func TestNodeAPI_aNodeWithNoKeyIsRefused(t *testing.T) {
	now := time.Now()
	body := []byte(`{}`)
	r := signedRequest(t, http.MethodPost, "/v1/internal/node/register", "node-a", body, now)

	perNode := func(nodeID string) (NodeStampVerifier, error) {
		if nodeID == "node-b" {
			return testVerifier(nodeID)
		}
		return nil, errNoSuchNodeForTest
	}
	if _, _, ok := VerifyNodeAPI(perNode, r, body, now); ok {
		t.Error("a node the resolver has no key for was accepted")
	}

	// And the same request, from the node the resolver does know, is accepted —
	// so the refusal above is the resolver's answer and not a broken stamp.
	rb := signedRequest(t, http.MethodPost, "/v1/internal/node/register", "node-b", body, now)
	if _, _, ok := VerifyNodeAPI(perNode, rb, body, now); !ok {
		t.Error("a node the resolver has a key for was refused")
	}
}

var errNoSuchNodeForTest = &noSuchNodeError{}

type noSuchNodeError struct{}

func (*noSuchNodeError) Error() string { return "no key for that node" }

// Every way a request can arrive without a usable stamp is a refusal, and none
// of them panics.
func TestNodeAPI_everyMalformedStampIsRefused(t *testing.T) {
	now := time.Now()
	body := []byte(`{}`)
	keys := testVerifier

	cases := map[string]func(*http.Request){
		"no headers at all": func(r *http.Request) {
			r.Header.Del(NodeIDHeader)
			r.Header.Del(NodeStampHeader)
		},
		"no node id": func(r *http.Request) { r.Header.Del(NodeIDHeader) },
		"no stamp":   func(r *http.Request) { r.Header.Del(NodeStampHeader) },
		"stamp with no separator": func(r *http.Request) {
			r.Header.Set(NodeStampHeader, "not-a-stamp")
		},
		"stamp with an unreadable time": func(r *http.Request) {
			_, sig, _ := strings.Cut(r.Header.Get(NodeStampHeader), ".")
			r.Header.Set(NodeStampHeader, "when."+sig)
		},
		"stamp with an unreadable mac": func(r *http.Request) {
			stamp, _, _ := strings.Cut(r.Header.Get(NodeStampHeader), ".")
			r.Header.Set(NodeStampHeader, stamp+".nothex")
		},
		"stamp of the right shape and the wrong value": func(r *http.Request) {
			r.Header.Set(NodeStampHeader, strconv.FormatInt(now.Unix(), 10)+".00ff")
		},
	}

	for name, break_ := range cases {
		t.Run(name, func(t *testing.T) {
			r := signedRequest(t, http.MethodPost, "/v1/internal/node/register", "node-a", body, now)
			break_(r)
			if _, _, ok := VerifyNodeAPI(keys, r, body, now); ok {
				t.Errorf("a request with %s was accepted", name)
			}
		})
	}

	// And with no resolver at all — a gateway with no cluster secret — nothing
	// is accepted rather than everything.
	r := signedRequest(t, http.MethodPost, "/v1/internal/node/register", "node-a", body, now)
	if _, _, ok := VerifyNodeAPI(nil, r, body, now); ok {
		t.Error("a request was accepted by a gateway with no key")
	}
}

// Signing refuses rather than producing a stamp nothing can use.
func TestNodeAPI_signingRefusesWithoutAKeyOrANode(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/v1/internal/node/register", nil)
	if err := SignNodeAPI(nil, r, "node-a", nil, time.Now()); err == nil {
		t.Error("signing with nothing to sign with succeeded")
	}
	if err := SignNodeAPI(testSigner, r, "  ", nil, time.Now()); err == nil {
		t.Error("signing with no node id succeeded")
	}
}

// A stamp that did not cover the query string could be lifted onto the same
// path with different parameters. Neither handler reads one today; the point is
// that the day one does, the omission is not silent.
func TestNodeAPI_aStampDoesNotCoverADifferentQuery(t *testing.T) {
	now := time.Now()
	body := []byte(`{}`)
	r := signedRequest(t, http.MethodPost, "/v1/internal/node/register?force=false", "node-a", body, now)

	r.URL.RawQuery = "force=true"
	if _, _, ok := VerifyNodeAPI(testVerifier, r, body, now); ok {
		t.Error("a stamp for one query string was accepted with another")
	}
}

// Caddy proxies every path to the gateway on loopback, so the source address of
// every public request is 127.0.0.1 and only the forwarding header tells the
// two apart. An endpoint that trusted loopback alone would be open to the
// world; one that refused it would refuse the node itself.
func TestIsNodeLocal_tellsAProcessOnThisHostFromTheInternet(t *testing.T) {
	cases := map[string]struct {
		remoteAddr string
		forwarded  string
		want       bool
	}{
		"a process on this host":         {"127.0.0.1:54321", "", true},
		"a process on this host, v6":     {"[::1]:54321", "", true},
		"another node on the overlay":    {"10.0.0.4:40000", "", true},
		"the internet, through Caddy":    {"127.0.0.1:54321", "198.51.100.9", false},
		"the internet, claiming to be":   {"127.0.0.1:54321", "127.0.0.1", false},
		"a direct connection off-node":   {"198.51.100.9:44444", "", false},
		"off-node with a header":         {"198.51.100.9:44444", "10.0.0.4", false},
		"an address that does not parse": {"not-an-address", "", false},
		"no port":                        {"127.0.0.1", "", false},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/v1/internal/node/register", nil)
			r.RemoteAddr = c.remoteAddr
			if c.forwarded != "" {
				r.Header.Set("X-Forwarded-For", c.forwarded)
			}
			if got := IsNodeLocal(r); got != c.want {
				t.Errorf("IsNodeLocal = %v, want %v", got, c.want)
			}
		})
	}

	if IsNodeLocal(nil) {
		t.Error("a nil request was read as a local caller")
	}
}

// otherNodeVerifier is a different node's key, for asserting that one node's
// stamp does not verify as another's.
func otherNodeVerifier(t *testing.T) NodeVerifierFor {
	t.Helper()
	key, err := NewNodeKeyPair()
	if err != nil {
		t.Fatalf("NewNodeKeyPair: %v", err)
	}
	pub, err := ParseNodePublicKey(key.PublicKey())
	if err != nil {
		t.Fatalf("ParseNodePublicKey: %v", err)
	}
	return func(string) (NodeStampVerifier, error) { return pub, nil }
}

// A resolver that cannot answer says so, separately from saying no — the
// gateway needs to tell "the cluster could not be read" from "somebody is
// forging stamps", and only one of those is worth waking anyone for.
func TestVerifyNodeAPI_anUnanswerableQuestionIsReportedAsWellAsRefused(t *testing.T) {
	now := time.Now()
	body := []byte(`{}`)
	r := signedRequest(t, http.MethodPost, "/v1/internal/node/register", "node-a", body, now)

	broken := func(string) (NodeStampVerifier, error) { return nil, errNoSuchNodeForTest }
	_, err, ok := VerifyNodeAPI(broken, r, body, now)
	if ok {
		t.Fatal("a request was accepted while the resolver could not answer")
	}
	if err == nil {
		t.Error("the resolver's failure was swallowed; it reads in the logs as a forged stamp")
	}

	// And a resolver that answers "nothing verifies for that node" is a
	// refusal, not a failure — a revoked node must not page anyone.
	nothing := func(string) (NodeStampVerifier, error) { return nil, nil }
	if _, err, ok := VerifyNodeAPI(nothing, r, body, now); ok || err != nil {
		t.Errorf("a node nothing verifies for gave ok=%v err=%v, want a plain refusal", ok, err)
	}
}
