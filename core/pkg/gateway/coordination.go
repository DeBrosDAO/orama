package gateway

import (
	"net/http"
	"time"

	nodeauth "github.com/DeBrosOfficial/network/pkg/auth"
)

// verifyCoordination reports whether a node-to-node coordination request came
// from inside the cluster.
//
// Two independent things have to hold: the request carries a MAC produced with
// a key derived from the cluster secret, and it arrived over the WireGuard
// overlay. The MAC is the credential — being on the overlay is not one, since
// every namespace's services are on that mesh — but the address check costs
// nothing and narrows what can even attempt a forgery.
//
// See pkg/auth/coordination.go for what the MAC covers.
func (g *Gateway) verifyCoordination(r *http.Request) bool {
	key, ok := g.coordinationKey(r)
	return ok && nodeauth.VerifyCoordination(key, r, time.Now(), g.cfg.NodePeerID)
}

// verifyCoordinationV2 is verifyCoordination for a route whose parameters
// travel in the body: only the v2 stamp, which covers the body, the audience (this
// node's peer id) and a single-use nonce, is accepted. The v1 stamp covers none of those, so a
// stripped-v2 replay with a swapped body would otherwise verify.
func (g *Gateway) verifyCoordinationV2(r *http.Request) bool {
	key, ok := g.coordinationKey(r)
	return ok && nodeauth.VerifyCoordinationV2(key, r, time.Now(), g.cfg.NodePeerID)
}

// coordinationKey is the MAC key for a request that arrived over the overlay.
func (g *Gateway) coordinationKey(r *http.Request) ([]byte, bool) {
	if !nodeauth.IsWireGuardPeer(r.RemoteAddr) || g.cfg == nil {
		return nil, false
	}
	key, err := nodeauth.CoordinationKey(g.cfg.ClusterSecret)
	if err != nil {
		return nil, false
	}
	return key, true
}
