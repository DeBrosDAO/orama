package auth

import (
	"errors"
	"net/http"

	authsvc "github.com/DeBrosOfficial/network/pkg/gateway/auth"
)

// ErrCodeNamespaceNotOwned is the machine-readable code for a login against a
// namespace that belongs to another wallet. A client that has to read the prose
// to tell "wrong namespace" from "the gateway is broken" cannot act on it.
const ErrCodeNamespaceNotOwned = "NAMESPACE_NOT_OWNED"

// writeCredentialError turns a credential-issuing failure into a response.
//
// A namespace owned by another wallet is the caller's answer, not a server
// fault: 403 with a code, and no credential of any kind in the body.
func writeCredentialError(w http.ResponseWriter, namespace string, err error) {
	var owned *authsvc.ErrNamespaceOwnedByAnother
	if errors.As(err, &owned) {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"error": "namespace " + owned.Namespace + " belongs to another wallet: " +
				"sign in with the wallet that owns it, or choose a namespace name nobody has taken",
			"code":      ErrCodeNamespaceNotOwned,
			"namespace": owned.Namespace,
		})
		return
	}
	writeError(w, http.StatusInternalServerError, err.Error())
}
