package gateway

import (
	"context"

	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
)

// Context keys for request-scoped values
const (
	ctxKeyAPIKey            = ctxkeys.APIKey
	ctxKeyJWT               = ctxkeys.JWT
	CtxKeyNamespaceOverride = ctxkeys.NamespaceOverride
	ctxKeyScopes            = ctxkeys.Scopes
	ctxKeyGrant             = ctxkeys.Grant
)

// hostNamespaceKey carries the namespace named by an ns-<name> host for a
// route the index gateway serves itself (a MainGateway route). The auth
// middleware replaces CtxKeyNamespaceOverride with the credential's own
// namespace, so without this the host's namespace was lost and a credential
// of A sent to ns-B acted on A.
type hostNamespaceKey struct{}

// withInternalAuth creates a context for internal gateway operations that bypass authentication.
// This is used when the gateway needs to make internal calls to services without auth checks.
func (g *Gateway) withInternalAuth(ctx context.Context) context.Context {
	return client.WithInternalAuth(ctx)
}
