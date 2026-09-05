package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/secrets"
)

// A node used to record its own existence by writing straight into the core
// cluster's tables: `INSERT INTO dns_nodes ...` against the rqlite handle it
// holds. That row is a promise every consumer trusts — `status = 'active' AND
// last_seen > ?` is what routes real traffic — and nothing checked who made it.
// There was no request to authenticate, so there was nowhere to put a
// credential and nothing to record.
//
// A node now asks the cluster to record it, over an endpoint, and the ask
// carries a MAC over what is being asked: the method, the path, the node the
// claim is about, and the body. The node id the handler acts on comes from the
// verified stamp and never from the body, so a caller cannot register a row for
// a node it is not.
//
// Today the key every node signs with is derived from the cluster secret, which
// every node holds — so this proves membership of the cluster, not identity
// within it, exactly as the coordination MAC does. That is deliberate: it is
// the same trust as the rqlite password these calls replace, so nothing about
// who may do what changes yet. The seam is the point. VerifyNodeAPI resolves
// the key *by node id*, so giving each node its own key is a change of
// resolver, not a change of protocol, of the endpoints, or of the callers.

const (
	// NodeIDHeader names the node a request is about.
	NodeIDHeader = "X-Orama-Node-ID"

	// NodeAPIMACHeader carries "<unix seconds>.<hex hmac>".
	NodeAPIMACHeader = "X-Orama-Node-MAC"

	// nodeAPIKeyPurpose is the HKDF domain separator. It keeps this key
	// unrelated to the coordination, internal-auth-hop, TURN, push and secrets
	// keys derived from the same cluster secret, so one of them leaking does
	// not hand over the others.
	nodeAPIKeyPurpose = "node-api"

	// nodeAPIMaxSkew bounds replay. These calls are one request over the
	// WireGuard mesh; a minute covers clock drift between nodes rather than
	// network delay.
	nodeAPIMaxSkew = 60 * time.Second
)

// NodeAPIKeyFor resolves the key a node signs with. Phase A of the node-identity
// work replaces the implementation, not the shape.
type NodeAPIKeyFor func(nodeID string) ([]byte, error)

// ClusterNodeAPIKey derives the key every node in the cluster shares.
//
// The secret is trimmed first, and that is load-bearing rather than tidy: it is
// read from a file, and one node's copy carrying a trailing newline while
// another's does not would derive two different keys, and every call between
// them would fail a MAC check for no visible reason (bugboard #837).
func ClusterNodeAPIKey(clusterSecret string) ([]byte, error) {
	key, err := secrets.DeriveKey(strings.TrimSpace(clusterSecret), nodeAPIKeyPurpose)
	if err != nil {
		return nil, fmt.Errorf("no node-api key: this node cannot prove which node it is: %w", err)
	}
	return key, nil
}

// SharedClusterKey is the resolver used while every node signs with the same
// key: it answers the same key for every node id.
//
// It takes the id and ignores it on purpose. That argument is what a per-node
// resolver reads, so the callers and the endpoints are already written against
// the shape identity will arrive in.
func SharedClusterKey(clusterSecret string) NodeAPIKeyFor {
	return func(string) ([]byte, error) { return ClusterNodeAPIKey(clusterSecret) }
}

// nodeAPIPayload is the exact string a MAC covers.
//
// The body is in it by hash: these requests carry their claim in the body — an
// IP address, a region, an operator wallet — so a MAC over method and path
// alone would be replayable onto a body of the attacker's choosing, which is
// the whole thing this is for. The node id is in it for the same reason, so a
// stamp cannot be lifted onto a request about a different node. The query
// string is in it because the sibling coordination MAC covers one and there is
// no reason for the two to promise different things: neither of these handlers
// reads a query parameter today, and the day one does, the omission would be
// silent.
func nodeAPIPayload(method, path, query, nodeID string, body []byte, ts int64) string {
	sum := sha256.Sum256(body)
	return strings.Join([]string{
		"orama-node-api-v1",
		strings.ToUpper(method),
		path,
		query,
		nodeID,
		hex.EncodeToString(sum[:]),
		strconv.FormatInt(ts, 10),
	}, "\n")
}

// SignNodeAPI stamps a request as coming from a named node.
//
// The body is passed rather than read off the request: the caller has it in
// hand, and reading r.Body here would consume the reader the transport is about
// to send.
func SignNodeAPI(key []byte, r *http.Request, nodeID string, body []byte, now time.Time) error {
	if len(key) == 0 {
		return fmt.Errorf("no node-api key: this node has no cluster secret, so it cannot prove " +
			"to the cluster which node it is")
	}
	if strings.TrimSpace(nodeID) == "" {
		return fmt.Errorf("no node id: a node-api request says which node it is about, and this one does not")
	}
	ts := now.Unix()
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(nodeAPIPayload(r.Method, r.URL.Path, r.URL.RawQuery, nodeID, body, ts)))
	r.Header.Set(NodeIDHeader, nodeID)
	r.Header.Set(NodeAPIMACHeader, strconv.FormatInt(ts, 10)+"."+hex.EncodeToString(mac.Sum(nil)))
	return nil
}

// VerifyNodeAPI reports which node stamped a request, if one did.
//
// The returned id is the one the handler must act on. It is read from the
// header the MAC covers, so it is as authenticated as the rest of the request;
// a body claiming a different node is the body being wrong, not the caller
// being someone else.
//
// It answers "" for every reason: no id, no stamp, a malformed stamp, no key
// for that node, a stale or future timestamp, or a MAC over a different request
// than the one that arrived. A caller is told which, but never in a way that
// distinguishes "no such node" from "wrong key" — see the handler.
func VerifyNodeAPI(keyFor NodeAPIKeyFor, r *http.Request, body []byte, now time.Time) (string, bool) {
	if keyFor == nil {
		return "", false
	}
	nodeID := strings.TrimSpace(r.Header.Get(NodeIDHeader))
	if nodeID == "" {
		return "", false
	}
	stamp, sig, ok := strings.Cut(strings.TrimSpace(r.Header.Get(NodeAPIMACHeader)), ".")
	if !ok {
		return "", false
	}
	ts, err := strconv.ParseInt(stamp, 10, 64)
	if err != nil {
		return "", false
	}
	// Both directions: a future timestamp is as much a sign of a forged stamp
	// as an old one.
	if skew := now.Sub(time.Unix(ts, 0)); skew > nodeAPIMaxSkew || skew < -nodeAPIMaxSkew {
		return "", false
	}
	key, err := keyFor(nodeID)
	if err != nil || len(key) == 0 {
		return "", false
	}
	presented, err := hex.DecodeString(sig)
	if err != nil {
		return "", false
	}
	expected := hmac.New(sha256.New, key)
	expected.Write([]byte(nodeAPIPayload(r.Method, r.URL.Path, r.URL.RawQuery, nodeID, body, ts)))
	if !hmac.Equal(presented, expected.Sum(nil)) {
		return "", false
	}
	return nodeID, true
}
