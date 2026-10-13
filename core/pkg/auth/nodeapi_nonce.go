package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// nodeAPIReplayTTL is how long a node-api nonce is remembered: a stamp is valid
// for nodeAPIMaxSkew in either direction, so a nonce that has been forgotten
// belongs to a stamp that would already be refused.
const nodeAPIReplayTTL = 2 * nodeAPIMaxSkew

// nodeAPIReplays remembers the nonces of the calls this process has served
// from nodes with a recorded key. It holds the node id with the nonce, so one
// node's nonce is never another's.
//
// A call verified against the key inside a peer id (enrolment, which any
// machine can make for an id it generated) is remembered in its own cache,
// nodeAPIEnrolReplays: a full cache refuses new nonces, and a flood of
// self-keyed enrolments must not be able to fill the one that heartbeats use.
var (
	nodeAPIReplays      = newReplayCache(coordinationReplayCapacity, nodeAPIReplayTTL)
	nodeAPIEnrolReplays = newReplayCache(coordinationReplayCapacity, nodeAPIReplayTTL)
)

func nodeAPIReplaysFor(verifier NodeStampVerifier) *replayCache {
	if _, identity := verifier.(*libp2pKey); identity {
		return nodeAPIEnrolReplays
	}
	return nodeAPIReplays
}

// nodeAPIPayloadNonced is nodeAPIPayload plus the nonce, under its own label so
// one signature is never valid as the other. A captured heartbeat or register
// is good for the one call it was made for: replaying it later would put back
// an address the node has since left.
func nodeAPIPayloadNonced(method, path, query, nodeID string, body []byte, nonce string, ts int64) string {
	sum := sha256.Sum256(body)
	return strings.Join([]string{
		"orama-node-api-v2",
		strings.ToUpper(method),
		path,
		query,
		nodeID,
		hex.EncodeToString(sum[:]),
		nonce,
		strconv.FormatInt(ts, 10),
	}, "\n")
}

// verifyNodeAPINonced checks the nonced stamp of node nodeID and consumes its
// nonce. The cache is per process and empty after a restart, so a stamp made
// before this process started is refused (madeAfterProcessStart): it cannot be
// told from a replay of one already served.
func verifyNodeAPINonced(verifier NodeStampVerifier, r *http.Request, body []byte, nodeID string, sig []byte, ts int64, now time.Time) bool {
	if !madeAfterProcessStart(ts, now) {
		return false
	}
	nonce := r.Header.Get(NodeNonceHeader)
	if raw, err := hex.DecodeString(nonce); err != nil || len(raw) != coordinationNonceBytes {
		return false
	}
	if !verifier.Verify([]byte(nodeAPIPayloadNonced(r.Method, r.URL.Path, r.URL.RawQuery, nodeID, body, nonce, ts)), sig) {
		return false
	}
	return nodeAPIReplaysFor(verifier).firstUse(nodeID+"\n"+nonce, now)
}
