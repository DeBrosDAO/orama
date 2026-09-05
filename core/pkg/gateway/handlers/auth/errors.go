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

// ErrCodeNamespaceUnowned is a login against a namespace nobody holds.
//
// It is a different answer from "somebody else owns it" and the next move is
// different too: there is nobody to ask for an invitation, and the name is
// taken so it cannot be created either. It happens only to namespaces left
// behind by the anonymous-creation path that bug-357 closed.
const ErrCodeNamespaceUnowned = "NAMESPACE_UNOWNED"

// ErrCodeNoKeysHere is minting a key in the lobby, which has none.
const ErrCodeNoKeysHere = "NAMESPACE_HAS_NO_KEYS"

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
	if errors.Is(err, authsvc.ErrNamespaceUnowned) {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"error": "namespace " + namespace + " has no owner, so nobody may sign in to it: " +
				"it was created by a path that no longer exists, and the platform has to adopt or remove it",
			"code":      ErrCodeNamespaceUnowned,
			"namespace": namespace,
		})
		return
	}
	if authsvc.IsLobbyNamespace(namespace) {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"error":     err.Error(),
			"code":      ErrCodeNoKeysHere,
			"namespace": namespace,
		})
		return
	}
	writeError(w, http.StatusInternalServerError, err.Error())
}
