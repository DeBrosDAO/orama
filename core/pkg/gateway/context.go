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
)

// withInternalAuth creates a context for internal gateway operations that bypass authentication.
// This is used when the gateway needs to make internal calls to services without auth checks.
func (g *Gateway) withInternalAuth(ctx context.Context) context.Context {
	return client.WithInternalAuth(ctx)
}
