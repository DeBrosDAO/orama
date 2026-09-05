package gateway

import "net/http"

// A 401 from this gateway had at least six distinct causes and told them apart
// only by an English string: "missing API key", "invalid API key", the auth
// error the proxy path assembled, the scope gate's "user authentication
// required", and two more. A client could not tell "you sent nothing" from
// "your key was revoked" from "your token expired" without matching on prose,
// so nobody did, and every one of them surfaced to a developer as the same
// unexplained 401. That is what cost days on bug-160 and bug-164.
//
// Every refusal now carries a code from this list, the sentence a person reads,
// and a hint saying what to do about it. The code is the contract: the SDK
// switches on it, and the list only ever grows.
const (
	// CodeAuthMissing — no credential at all was presented.
	CodeAuthMissing = "AUTH_MISSING"
	// CodeAuthInvalidKey — a credential was presented and is not one this
	// cluster knows.
	CodeAuthInvalidKey = "AUTH_INVALID_KEY"
	// CodeDestinationNotAllowed — the credential is fine; the destination is
	// not one this route will reach. Distinct from every other code here
	// because retrying with a different credential will not help.
	CodeDestinationNotAllowed = "DESTINATION_NOT_ALLOWED"
	// CodeAuthRevoked — the credential was valid and has been revoked.
	CodeAuthRevoked = "AUTH_REVOKED"
	// CodeAuthExpired — the credential was valid and has expired.
	CodeAuthExpired = "AUTH_EXPIRED"
	// CodeAuthUserJWTRequired — the grant is held but this operation needs a
	// logged-in user, not a bare key.
	CodeAuthUserJWTRequired = "USER_JWT_REQUIRED"
	// CodeScopeMissing — the credential is valid and lacks the grant this
	// route requires. The required grant is in the `required_scope` field.
	//
	// The wire value is the one already shipped: the SDK turns it into a
	// ScopeError, and renaming it would break every client that does. The
	// audit proposed SCOPE_MISSING; what matters is that the code is stable
	// and written down, not which spelling it has.
	CodeScopeMissing = "INSUFFICIENT_SCOPE"
	// CodeAuthUserJWTRequiredWire is likewise the shipped spelling.
	CodeAuthUserJWTRequiredWire = "USER_JWT_REQUIRED"
	// CodeNamespaceMismatch — the credential belongs to another namespace.
	CodeNamespaceMismatch = "NAMESPACE_MISMATCH"
	// CodeOwnershipRequired — the credential is valid, in the right namespace,
	// and is not an owner of it.
	CodeOwnershipRequired = "OWNERSHIP_REQUIRED"
	// CodeOperatorRequired — the route is the cluster operator's. Also the
	// shipped spelling.
	CodeOperatorRequired = "NOT_AN_OPERATOR"
)

// authHints are what to do about each refusal. They are here rather than at the
// call sites so the same cause reads the same way wherever it is refused.
var authHints = map[string]string{
	CodeAuthMissing:           "send the credential in an Authorization header, or X-API-Key",
	CodeAuthInvalidKey:        "check the key is for this cluster and has not been deleted",
	CodeAuthRevoked:           "this credential or session was revoked; sign in again or use a new key",
	CodeAuthExpired:           "refresh the token, or sign in again",
	CodeAuthUserJWTRequired:   "exchange the key for a token, or sign in as a user",
	CodeScopeMissing:          "mint a key with the grant named in required_scope",
	CodeNamespaceMismatch:     "use a credential belonging to this namespace",
	CodeOwnershipRequired:     "use the namespace owner's credential, or an admin key for this namespace",
	CodeOperatorRequired:      "this is a cluster operator's route; an admin key for a namespace is not enough",
	CodeDestinationNotAllowed: "a different credential will not help; the destination itself is refused",
}

// writeAuthError writes a refusal a client can act on without reading English.
//
// extra carries anything specific to the cause — `required_scope` for a missing
// grant, the namespaces involved in a mismatch.
func writeAuthError(w http.ResponseWriter, status int, code, message string, extra map[string]any) {
	body := map[string]any{
		"error": message,
		"code":  code,
	}
	if hint := authHints[code]; hint != "" {
		body["hint"] = hint
	}
	for k, v := range extra {
		body[k] = v
	}
	if status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", `Bearer realm="gateway", charset="UTF-8"`)
	}
	writeJSON(w, status, body)
}

// unauthorized and forbidden are the two shapes every refusal takes.
func unauthorized(w http.ResponseWriter, code, message string, extra map[string]any) {
	writeAuthError(w, http.StatusUnauthorized, code, message, extra)
}

func forbidden(w http.ResponseWriter, code, message string, extra map[string]any) {
	writeAuthError(w, http.StatusForbidden, code, message, extra)
}
