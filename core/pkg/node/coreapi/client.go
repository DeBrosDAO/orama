// Package coreapi is how a node records facts about itself in the core
// cluster.
//
// A node used to write those rows itself, with the rqlite handle it holds. It
// now asks, over the index gateway running on the same host, and the ask
// carries a stamp naming which node it is from. The gateway is reached on
// loopback rather than over the mesh on purpose: the node's DNS registration
// already waits for the local gateway to be up (it is a declared dependency of
// that boot component), so this adds no ordering that was not there, and a call
// that never leaves the host cannot be observed on the overlay.
//
// On the supported upgrade path the two are the same version: the upgrade stops
// every `orama-namespace-*@*` unit — the index gateway included — before it
// replaces the binaries, and the restarted `orama-node` spawns the gateway
// again from the new one.
//
// That is a guarantee about the upgrade, not about every restart. `orama node
// rollout` pushes new binaries to the whole fleet before it walks the restarts,
// so for the length of that walk a node that restarts for its own reasons — a
// crash, an OOM kill, an operator — comes up new against a gateway that is
// still running and still old, and its registration is answered 404 until the
// gateway is bounced. The heartbeat retries every 30 seconds and the node
// recovers on its own; it is a bounded window, not a stuck state.
package coreapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/auth"
	"github.com/DeBrosOfficial/network/pkg/nodeapi"
	"github.com/libp2p/go-libp2p/core/crypto"
)

// requestTimeout bounds one call. These run on a 30-second heartbeat, so a call
// that has not been answered in ten seconds is one to abandon rather than one
// to keep waiting on.
const requestTimeout = 10 * time.Second

// Client posts a node's claims about itself to the index gateway.
type Client struct {
	baseURL string
	nodeID  string
	// own is this node's own key. It signs everything once the cluster has
	// recorded it.
	own *auth.NodeKeyPair
	// identity is this node's libp2p key. It signs the one call that records
	// `own`, and the cluster checks it against the public key carried inside
	// the peer id — so that call is authenticated with nothing recorded, and
	// with nothing any other machine holds.
	identity auth.NodeStampSigner
	http     *http.Client
	now      func() time.Time
}

// New builds a client for one node.
//
// It fails rather than returning a client that cannot sign: a node with no key
// and no identity cannot prove which node it is, and a caller that got a
// working client back would discover that one request at a time, in a warning
// log.
func New(baseURL, nodeID string, own *auth.NodeKeyPair, identity crypto.PrivKey) (*Client, error) {
	if strings.TrimSpace(baseURL) == "" {
		return nil, fmt.Errorf("no gateway address: this node has nowhere to record itself")
	}
	if strings.TrimSpace(nodeID) == "" {
		return nil, fmt.Errorf("no node id: this node cannot say which node it is")
	}
	if own == nil {
		return nil, fmt.Errorf("this node has no key of its own, so it cannot identify itself to the cluster")
	}
	signer := auth.NodeIdentitySigner(identity)
	if signer == nil {
		return nil, fmt.Errorf("this node has no libp2p identity, so it cannot prove which node it is")
	}
	return &Client{
		baseURL:  strings.TrimSuffix(baseURL, "/"),
		nodeID:   nodeID,
		own:      own,
		identity: signer,
		http:     &http.Client{Timeout: requestTimeout},
		now:      time.Now,
	}, nil
}

// EnrolKey asks the cluster to record this node's own key.
//
// It is stamped with this node's libp2p identity, because that is the one thing
// the cluster can check having been told nothing in advance: the public half is
// carried inside the peer id. Every other call is stamped with the key this
// records.
//
// Re-asserting the same key is not a change and is not an error: a node calls
// this on every start, and on every start after the first the cluster already
// has the row.
func (c *Client) EnrolKey(ctx context.Context) error {
	body, err := c.postSignedWith(ctx, c.identity, nodeapi.PathEnrolKey, nodeapi.EnrolKeyRequest{
		PublicKey: c.own.PublicKey(),
	})
	if err != nil {
		return err
	}
	var resp nodeapi.EnrolKeyResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return fmt.Errorf("the gateway's enrolment answer could not be read: %w", err)
	}
	return nil
}

// Register records this node, or updates the record it already has.
func (c *Client) Register(ctx context.Context, req nodeapi.RegisterRequest) error {
	_, err := c.post(ctx, nodeapi.PathRegister, req)
	return err
}

// errRefused is a request the cluster would not authenticate.
var errRefused = errors.New("the cluster did not recognise this node")

// Heartbeat refreshes this node's liveness and reports whether it is registered
// at all.
//
// A false answer is not a failure: it is a node whose registration never
// landed, or was reaped while it was restarting, and the caller registers.
func (c *Client) Heartbeat(ctx context.Context) (bool, error) {
	body, err := c.post(ctx, nodeapi.PathHeartbeat, struct{}{})
	if err != nil {
		return false, err
	}
	var resp nodeapi.HeartbeatResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return false, fmt.Errorf("the gateway's heartbeat answer could not be read: %w", err)
	}
	return resp.Registered, nil
}

// post signs and sends one request.
// post signs with this node's own key and, if the cluster says it does not
// know that key, records it again and retries once.
//
// The row can disappear under a running node: a core database restored from a
// backup taken before this node enrolled, an operator clearing it, a rebuilt
// registry. Without this the node would sign with a key the cluster no longer
// has, be refused every 30 seconds forever, and be reaped out of DNS — with a
// restart the only cure, which is the one thing the boot supervisor is built
// never to require.
//
// Re-enrolling is safe because enrolment is authenticated by this node's libp2p
// identity: only the real machine can put a key back for its own peer id. It is
// tried once, so a cluster that refuses for any other reason surfaces that
// refusal rather than looping.
func (c *Client) post(ctx context.Context, path string, payload any) ([]byte, error) {
	body, err := c.postSignedWith(ctx, c.own, path, payload)
	if !errors.Is(err, errRefused) {
		return body, err
	}
	if enrolErr := c.EnrolKey(ctx); enrolErr != nil {
		return nil, fmt.Errorf("this node's key is not the one the cluster has, and recording it "+
			"again failed: %w", enrolErr)
	}
	return c.postSignedWith(ctx, c.own, path, payload)
}

// postSignedWith signs and sends one request with a given credential.
func (c *Client) postSignedWith(ctx context.Context, signer auth.NodeStampSigner, path string, payload any) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode %s request: %w", path, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build %s request: %w", path, err)
	}
	req.Header.Set("Content-Type", "application/json")

	// The stamp covers the body, so it is signed after the body is fixed and
	// the same bytes are what the reader above will send.
	if err := auth.SignNodeAPI(signer, req, c.nodeID, body, c.now()); err != nil {
		return nil, err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call %s on the index gateway at %s: %w", path, c.baseURL, err)
	}
	defer resp.Body.Close()

	answer, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, fmt.Errorf("read the answer to %s: %w", path, err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("%s was refused by the index gateway at %s: %s: %w",
			path, c.baseURL, strings.TrimSpace(string(answer)), errRefused)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s was refused by the index gateway at %s: %s: %s",
			path, c.baseURL, resp.Status, strings.TrimSpace(string(answer)))
	}
	return answer, nil
}
