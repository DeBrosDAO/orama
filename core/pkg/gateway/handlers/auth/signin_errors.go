package auth

import (
	"errors"
	"net/http"

	authsvc "github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/auth/siw"
)

const (
	// ErrCodeMessageMalformed is the code for a message that is not a sign-in
	// message this gateway can read at all.
	ErrCodeMessageMalformed = "AUTH_MESSAGE_MALFORMED"

	// ErrCodeDomainMismatch is the code for a message signed for a different
	// host. This is the refusal the whole message format exists to make
	// possible: a signature collected elsewhere does not log in here.
	ErrCodeDomainMismatch = "AUTH_DOMAIN_MISMATCH"

	// ErrCodeMessageExpired is the code for a message whose own Expiration
	// Time has passed. Safe to report precisely: the deadline is in the bytes
	// the caller already holds.
	ErrCodeMessageExpired = "AUTH_MESSAGE_EXPIRED"

	// ErrCodeSignatureInvalid is the code for a message that is fine and a
	// signature over it that is not.
	ErrCodeSignatureInvalid = "AUTH_SIGNATURE_INVALID"

	// ErrCodeChallengeInvalid is the code for a challenge that cannot be
	// claimed: never issued, already used, or expired.
	//
	// One code for three causes, deliberately. Telling them apart would make
	// the endpoint an oracle for which wallets hold outstanding challenges,
	// and the caller's next move is the same in all three: ask for a new one.
	ErrCodeChallengeInvalid = "AUTH_CHALLENGE_INVALID"
)

var signInHints = map[string]string{
	ErrCodeMessageMalformed: "send the message from /v1/auth/challenge verbatim; it is signed byte for byte",
	ErrCodeDomainMismatch:   "the message names another host; ask this gateway for the challenge",
	ErrCodeMessageExpired:   "ask for a new challenge and sign that one",
	ErrCodeSignatureInvalid: "sign the message text exactly as issued, with the wallet it names",
	ErrCodeChallengeInvalid: "ask for a new challenge; each one is good once",
}

// writeSignInError turns a refused sign-in into a 401 a client can act on.
func writeSignInError(w http.ResponseWriter, err error) {
	code := ErrCodeSignatureInvalid
	switch {
	case errors.Is(err, siw.ErrDomainMismatch):
		code = ErrCodeDomainMismatch
	case errors.Is(err, siw.ErrExpired), errors.Is(err, siw.ErrNotYetValid), errors.Is(err, siw.ErrIssuedInFuture):
		code = ErrCodeMessageExpired
	case errors.Is(err, authsvc.ErrChallengeMessage):
		code = ErrCodeMessageMalformed
	}
	writeSignInRefusal(w, code, err.Error())
}

// writeChallengeConsumeError turns a challenge that could not be claimed into a
// response. A registry that could not be reached is a 503, not a bad signature.
func writeChallengeConsumeError(w http.ResponseWriter, err error) {
	if errors.Is(err, authsvc.ErrNonceInvalid) {
		writeSignInRefusal(w, ErrCodeChallengeInvalid, authsvc.ErrNonceInvalid.Error())
		return
	}
	writeError(w, http.StatusServiceUnavailable, authsvc.ErrNonceTransient.Error())
}

func writeSignInRefusal(w http.ResponseWriter, code, message string) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="gateway", charset="UTF-8"`)
	body := map[string]any{"error": message, "code": code}
	if hint := signInHints[code]; hint != "" {
		body["hint"] = hint
	}
	writeJSON(w, http.StatusUnauthorized, body)
}
