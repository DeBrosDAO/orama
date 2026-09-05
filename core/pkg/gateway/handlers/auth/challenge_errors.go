package auth

import (
	"errors"
	"net/http"

	authsvc "github.com/DeBrosOfficial/network/pkg/gateway/auth"
)

const (
	// ErrCodeNamespaceUnknown is the machine-readable code for a challenge
	// naming a namespace that does not exist.
	ErrCodeNamespaceUnknown = "NAMESPACE_UNKNOWN"

	// ErrCodeTooManyChallenges is the code for a wallet holding as many
	// unanswered challenges as it is allowed.
	ErrCodeTooManyChallenges = "TOO_MANY_CHALLENGES"
)

// writeChallengeError turns a failed challenge into a response a client can act
// on rather than a 500.
//
// A challenge for an unknown namespace used to create it. That is now the
// caller's answer — the namespace has to be created deliberately first — so it
// is a 404 with a code and a sentence saying what to do, not a server error.
func writeChallengeError(w http.ResponseWriter, namespace string, err error) {
	var unknown *authsvc.ErrNamespaceUnknown
	if errors.As(err, &unknown) {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"error": "namespace " + unknown.Namespace + " does not exist: create it with " +
				"`orama namespace create " + unknown.Namespace + "`, or sign in without a " +
				"namespace and create it from there",
			"code":      ErrCodeNamespaceUnknown,
			"namespace": unknown.Namespace,
		})
		return
	}

	var tooMany *authsvc.ErrTooManyOutstandingNonces
	if errors.As(err, &tooMany) {
		w.Header().Set("Retry-After", "300")
		writeJSON(w, http.StatusTooManyRequests, map[string]any{
			"error": "this wallet already has the maximum number of unanswered challenges " +
				"in namespace " + tooMany.Namespace + "; answer one or wait for them to expire",
			"code":      ErrCodeTooManyChallenges,
			"namespace": tooMany.Namespace,
		})
		return
	}

	writeError(w, http.StatusInternalServerError, err.Error())
}
