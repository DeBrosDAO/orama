package deployments

import (
	"fmt"
	"net/http"
	"time"

	nodeauth "github.com/DeBrosOfficial/network/pkg/auth"
)

// SetCoordinationSecret sets the cluster secret the replica coordination calls
// this node sends and receives are authenticated with.
func (s *DeploymentService) SetCoordinationSecret(secret string) {
	s.coordinationSecret = secret
}

// signReplicaRequest stamps a replica coordination request for the node whose
// peer id is audience. The endpoints it reaches set up, replace and tear down a
// namespace's replica, so the stamp has to be one only a holder of the cluster
// secret can make and only that node accepts (pkg/auth/coordination.go).
func (s *DeploymentService) signReplicaRequest(r *http.Request, audience string) error {
	key, err := nodeauth.CoordinationKey(s.coordinationSecret)
	if err != nil {
		return fmt.Errorf("cannot sign the replica request to node %s: %w", audience, err)
	}
	return nodeauth.SignCoordination(key, r, time.Now(), audience)
}

// isInternalRequest reports whether r is a replica coordination call from
// another node of this cluster, made for this node.
//
// Three things have to hold: it arrived from the WireGuard overlay, it carries
// a v2 coordination stamp (which covers the body and is single-use) made with
// the cluster secret, and the stamp was signed for this node's peer id. The
// overlay is no credential: every namespace's services are on it.
func (h *ReplicaHandler) isInternalRequest(r *http.Request) bool {
	if !nodeauth.IsWireGuardPeer(r.RemoteAddr) {
		return false
	}
	key, err := nodeauth.CoordinationKey(h.service.coordinationSecret)
	if err != nil {
		return false
	}
	return nodeauth.VerifyCoordinationV2(key, r, time.Now(), h.service.nodePeerID)
}
