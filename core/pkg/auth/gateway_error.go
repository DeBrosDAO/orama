package auth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// What a refusal means, in words somebody can act on.
//
// The gateway answers a refused request with a machine-readable code and a
// sentence. The CLI printed the sentence, which describes what the gateway
// decided and not what to do about it: "insufficient scope" tells you the
// request was refused, and leaves you to work out that the fix is a key minted
// with a different `--scope`. The code is the part worth switching on, and it
// is what this maps.

// GatewayError is a refusal, with its code kept.
type GatewayError struct {
	Status  int
	Code    string
	Message string
	// Namespace is the namespace the credential belongs to, when the gateway
	// says — the field that makes a namespace mismatch legible.
	Namespace string
	// CredentialNamespace is the namespace that was asked for.
	CredentialNamespace string
}

func (e *GatewayError) Error() string {
	if advice := gatewayErrorAdvice(e); advice != "" {
		return advice
	}
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("the gateway refused this request (HTTP %d)", e.Status)
}

// The codes the gateway sends. They are duplicated here rather than imported
// because the CLI talks to gateways it was not built alongside: a code this
// binary does not recognise has to fall through to the gateway's own sentence,
// not fail to compile.
const (
	CodeAuthMissing       = "AUTH_MISSING"
	CodeAuthInvalidKey    = "AUTH_INVALID_KEY"
	CodeAuthRevoked       = "AUTH_REVOKED"
	CodeAuthExpired       = "AUTH_EXPIRED"
	CodeScopeMissing      = "INSUFFICIENT_SCOPE"
	CodeUserJWTRequired   = "USER_JWT_REQUIRED"
	CodeNamespaceMismatch = "NAMESPACE_MISMATCH"
	CodeOwnershipRequired = "OWNERSHIP_REQUIRED"
	CodeOperatorRequired  = "NOT_AN_OPERATOR"
)

// gatewayErrorAdvice is the sentence that says what to do next.
//
// A code with nothing useful to add is absent from here on purpose: repeating
// the gateway's own message with different words would be worse than passing it
// through.
func gatewayErrorAdvice(e *GatewayError) string {
	switch e.Code {
	case CodeAuthMissing:
		return "this command needs a credential: run 'orama auth login', or set ORAMA_TOKEN in CI"
	case CodeAuthInvalidKey:
		return "the gateway does not recognise this credential: run 'orama auth login' to get a new one"
	case CodeAuthRevoked:
		return "this credential has been revoked: run 'orama auth login', or mint a new key with " +
			"'orama namespace keys create --scope ...'"
	case CodeAuthExpired:
		return "this credential has expired: run 'orama auth login'"
	case CodeScopeMissing:
		if missing := scopeFromMessage(e.Message); missing != "" {
			return fmt.Sprintf("this credential does not hold %s; mint one that does with "+
				"'orama namespace keys create --scope %s'", missing, missing)
		}
		return "this credential does not hold what this command needs; " +
			"'orama auth whoami' shows what it does hold"
	case CodeUserJWTRequired:
		return "this route needs a signed-in wallet rather than an API key: run 'orama auth login'"
	case CodeNamespaceMismatch:
		if e.Namespace != "" && e.CredentialNamespace != "" {
			return fmt.Sprintf("this credential belongs to namespace %q and the request was for %q; "+
				"switch with 'orama auth switch', or point the CLI at the right gateway",
				e.CredentialNamespace, e.Namespace)
		}
		return "this credential belongs to a different namespace than the request; 'orama auth whoami' shows which"
	case CodeOwnershipRequired:
		return "only the namespace's owner may do this; 'orama members list' shows who that is"
	case CodeOperatorRequired:
		return "this is an operator command and this wallet is not on the operator list"
	}
	return ""
}

// scopeFromMessage pulls the grant out of a message shaped like
// "insufficient scope: storage".
func scopeFromMessage(message string) string {
	_, after, found := strings.Cut(message, ":")
	if !found {
		return ""
	}
	scope := strings.TrimSpace(after)
	// Anything with a space in it is a sentence, not a grant name.
	if scope == "" || strings.ContainsAny(scope, " \t") {
		return ""
	}
	return scope
}

// GatewayErrorFrom reads a refusal.
//
// A body that is not the gateway's error shape — an HTML page from something in
// front of it, an empty 502 — becomes an error naming the status, because
// silently reporting "the gateway refused" for what is actually a proxy in the
// way sends people to the wrong place.
func GatewayErrorFrom(status int, body []byte) *GatewayError {
	out := &GatewayError{Status: status}

	var parsed struct {
		Error               string `json:"error"`
		Message             string `json:"message"`
		Code                string `json:"code"`
		Namespace           string `json:"namespace"`
		CredentialNamespace string `json:"credential_namespace"`
	}
	if err := json.Unmarshal(body, &parsed); err == nil {
		out.Code = strings.TrimSpace(parsed.Code)
		out.Namespace = strings.TrimSpace(parsed.Namespace)
		out.CredentialNamespace = strings.TrimSpace(parsed.CredentialNamespace)
		out.Message = strings.TrimSpace(parsed.Error)
		if out.Message == "" {
			out.Message = strings.TrimSpace(parsed.Message)
		}
	}
	if out.Message == "" {
		snippet := strings.TrimSpace(string(body))
		if len(snippet) > 200 {
			snippet = snippet[:200] + "…"
		}
		if snippet == "" {
			out.Message = fmt.Sprintf("HTTP %d with an empty body", status)
		} else {
			out.Message = fmt.Sprintf("HTTP %d: %s", status, snippet)
		}
	}
	return out
}

// IsUnauthorized reports whether a refusal is about the credential rather than
// the request, which is what decides whether retrying could ever work.
func (e *GatewayError) IsUnauthorized() bool {
	if e == nil {
		return false
	}
	switch e.Code {
	case CodeAuthMissing, CodeAuthInvalidKey, CodeAuthRevoked, CodeAuthExpired:
		return true
	}
	return e.Status == http.StatusUnauthorized
}
