package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
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
// A node signs with an Ed25519 key it generated itself and that never leaves
// the machine. The cluster records only the public half, so it holds nothing
// that can impersonate a node — and one node's disk is one node, rather than a
// working credential for the whole fleet.
//
// Recording that key is authenticated too, by the node's libp2p identity: a
// peer id carries its own public key, so the cluster can check who is enrolling
// having been told nothing in advance. Nothing derived from the cluster secret
// is accepted on any of these calls, so a machine holding every shared secret
// the cluster has still cannot speak as a node it is not.

const (
	// NodeIDHeader names the node a request is about.
	NodeIDHeader = "X-Orama-Node-ID"

	// NodeStampHeader carries "<unix seconds>.<hex signature>".
	NodeStampHeader = "X-Orama-Node-Stamp"

	// nodeAPIMaxSkew bounds replay. These calls are one request to the gateway
	// on the node's own host; a minute covers clock drift rather than network
	// delay.
	nodeAPIMaxSkew = 60 * time.Second
)

// NodeStampSigner makes a stamp. A node holds one.
type NodeStampSigner interface {
	Sign(payload []byte) ([]byte, error)
}

// NodeStampVerifier checks a stamp. The cluster resolves one per node.
//
// It answers a bool rather than an error on purpose: "this is not a valid stamp
// for that node" is the only thing a caller may learn, and a typed reason would
// be an oracle for which nodes exist and which keys are wrong.
type NodeStampVerifier interface {
	Verify(payload, sig []byte) bool
}

// NodeVerifierFor says how to check a stamp claiming to be from a node.
//
// It returns nil, nil for a node nothing can verify — one whose credential was
// revoked — which is a refusal, not an error. An error means the question could
// not be answered at all, which is also a refusal, but one worth logging.
type NodeVerifierFor func(nodeID string) (NodeStampVerifier, error)

// NodeKeyPair is a node's own Ed25519 key. The private half never leaves the
// machine that generated it, so the cluster holds nothing that can impersonate
// the node — which is the property the shared secret could not have.
type NodeKeyPair struct{ priv ed25519.PrivateKey }

// NewNodeKeyPair generates one.
func NewNodeKeyPair() (*NodeKeyPair, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate this node's key: %w", err)
	}
	return &NodeKeyPair{priv: priv}, nil
}

// NodeKeyPairFrom adopts an existing private key.
func NodeKeyPairFrom(priv ed25519.PrivateKey) (*NodeKeyPair, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("this node's key is %d bytes, not an Ed25519 private key", len(priv))
	}
	return &NodeKeyPair{priv: priv}, nil
}

// PrivateKey is the raw key, for writing it to disk.
func (k *NodeKeyPair) PrivateKey() ed25519.PrivateKey { return k.priv }

// PublicKey is what the cluster records, base64 of the 32 raw bytes.
func (k *NodeKeyPair) PublicKey() string {
	return base64.StdEncoding.EncodeToString(k.priv.Public().(ed25519.PublicKey))
}

func (k *NodeKeyPair) Sign(payload []byte) ([]byte, error) {
	return ed25519.Sign(k.priv, payload), nil
}

// NodePublicKey verifies stamps against a recorded public key.
type NodePublicKey struct{ pub ed25519.PublicKey }

// ParseNodePublicKey reads a public key as it is stored.
func ParseNodePublicKey(encoded string) (*NodePublicKey, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return nil, fmt.Errorf("this node's public key is not base64: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("this node's public key is %d bytes, not an Ed25519 public key", len(raw))
	}
	return &NodePublicKey{pub: ed25519.PublicKey(raw)}, nil
}

func (p *NodePublicKey) Verify(payload, sig []byte) bool {
	return ed25519.Verify(p.pub, payload, sig)
}

// NodeIdentityVerifier resolves a node id to the key inside it.
//
// Recording a node's key is the one moment where the cluster decides which
// machine answers to a node id, so it must not be decided by anything a
// different machine also holds. Every node holds the cluster secret, so if that
// were enough, a compromised node could enrol its own key for a node id that
// had not booted yet — impersonating that node from then on, and locking the
// real one out permanently, because its key would no longer match and re-keying
// is refused.
//
// A node id is a libp2p peer id, and for the Ed25519 identities this fleet uses
// the public key is carried inside the id itself. So there is already a key
// that only the real node holds, and the cluster can check against it having
// been told nothing in advance. That is what authenticates an enrolment, and it
// is why there is no trust-on-first-use window at all: the only machine that
// can enrol a key for node X is the one holding X's identity.
func NodeIdentityVerifier(nodeID string) (NodeStampVerifier, error) {
	id, err := peer.Decode(nodeID)
	if err != nil {
		return nil, fmt.Errorf("%q is not a node id: %w", nodeID, err)
	}
	pub, err := id.ExtractPublicKey()
	if err != nil || pub == nil {
		// An id whose key is not carried inside it — an older, hashed identity.
		// There is nothing to check against, and treating that as verified
		// would make the check optional for anyone who could produce one.
		return nil, fmt.Errorf("the key of node %s is not carried in its id, so it cannot prove which node it is", nodeID)
	}
	return &libp2pKey{pub: pub}, nil
}

// NodeIdentitySigner signs with a node's libp2p identity key.
func NodeIdentitySigner(identity crypto.PrivKey) NodeStampSigner {
	if identity == nil {
		return nil
	}
	return &libp2pKey{priv: identity}
}

// libp2pKey adapts a libp2p key pair to the stamp interfaces.
type libp2pKey struct {
	priv crypto.PrivKey
	pub  crypto.PubKey
}

func (k *libp2pKey) Sign(payload []byte) ([]byte, error) {
	sig, err := k.priv.Sign(payload)
	if err != nil {
		return nil, fmt.Errorf("sign with this node's libp2p identity: %w", err)
	}
	return sig, nil
}

func (k *libp2pKey) Verify(payload, sig []byte) bool {
	ok, err := k.pub.Verify(payload, sig)
	return err == nil && ok
}

// nodeAPIPayload is the exact string a stamp covers.
//
// The body is in it by hash: these requests carry their claim in the body — an
// IP address, an operator wallet, a public key — so a stamp over method and
// path alone would be replayable onto a body of the attacker's choosing, which
// is the whole thing this is for. The node id is in it for the same reason, so
// a stamp cannot be lifted onto a request about a different node. The query
// string is in it because the sibling coordination MAC covers one and there is
// no reason for the two to promise different things: no handler here reads a
// query parameter today, and the day one does, the omission would be silent.
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
func SignNodeAPI(signer NodeStampSigner, r *http.Request, nodeID string, body []byte, now time.Time) error {
	if signer == nil {
		return fmt.Errorf("this node has nothing to sign with, so it cannot prove to the cluster which node it is")
	}
	if strings.TrimSpace(nodeID) == "" {
		return fmt.Errorf("no node id: a node-api request says which node it is about, and this one does not")
	}
	ts := now.Unix()
	sig, err := signer.Sign([]byte(nodeAPIPayload(r.Method, r.URL.Path, r.URL.RawQuery, nodeID, body, ts)))
	if err != nil {
		return err
	}
	r.Header.Set(NodeIDHeader, nodeID)
	r.Header.Set(NodeStampHeader, strconv.FormatInt(ts, 10)+"."+hex.EncodeToString(sig))
	return nil
}

// VerifyNodeAPI reports which node stamped a request, if one did.
//
// The returned id is the one the handler must act on. It is read from the
// header the stamp covers, so it is as authenticated as the rest of the
// request; a body claiming a different node is the body being wrong, not the
// caller being someone else.
//
// It answers "" for every reason: no id, no stamp, a malformed stamp, nothing
// that can verify for that node, a stale or future timestamp, or a stamp over a
// different request than the one that arrived. A caller is never told which, so
// there is no oracle distinguishing "no such node" from "wrong key".
//
// The error is separate from the verdict, and is not for the caller: it says
// the question could not be answered rather than that the answer was no. The
// gateway logs it; the caller is refused either way.
func VerifyNodeAPI(verifierFor NodeVerifierFor, r *http.Request, body []byte, now time.Time) (string, error, bool) {
	if verifierFor == nil {
		return "", nil, false
	}
	nodeID := strings.TrimSpace(r.Header.Get(NodeIDHeader))
	if nodeID == "" {
		return "", nil, false
	}
	stamp, sig, ok := strings.Cut(strings.TrimSpace(r.Header.Get(NodeStampHeader)), ".")
	if !ok {
		return "", nil, false
	}
	ts, err := strconv.ParseInt(stamp, 10, 64)
	if err != nil {
		return "", nil, false
	}
	// Both directions: a future timestamp is as much a sign of a forged stamp
	// as an old one.
	if skew := now.Sub(time.Unix(ts, 0)); skew > nodeAPIMaxSkew || skew < -nodeAPIMaxSkew {
		return "", nil, false
	}
	verifier, err := verifierFor(nodeID)
	if err != nil {
		// The caller still gets one refusal — telling it apart from a bad stamp
		// would be an oracle — but the gateway needs to know, because a
		// database outage otherwise reads in the logs as somebody forging
		// stamps and sends whoever is on call to the wrong place.
		return "", err, false
	}
	if verifier == nil {
		return "", nil, false
	}
	presented, err := hex.DecodeString(sig)
	if err != nil {
		return "", nil, false
	}
	if !verifier.Verify([]byte(nodeAPIPayload(r.Method, r.URL.Path, r.URL.RawQuery, nodeID, body, ts)), presented) {
		return "", nil, false
	}
	return nodeID, nil, true
}
