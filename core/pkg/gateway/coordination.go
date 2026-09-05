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
	if !nodeauth.IsWireGuardPeer(r.RemoteAddr) {
		return false
	}
	if g.cfg == nil {
		return false
	}
	key, err := nodeauth.CoordinationKey(g.cfg.ClusterSecret)
	if err != nil {
		return false
	}
	return nodeauth.VerifyCoordination(key, r, time.Now())
}
