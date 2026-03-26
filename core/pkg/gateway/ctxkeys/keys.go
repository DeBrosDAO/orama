package ctxkeys

// ContextKey is used for storing request-scoped authentication and metadata in context
type ContextKey string

const (
	// APIKey stores the API key string extracted from the request
	APIKey ContextKey = "api_key"

	// JWT stores the validated JWT claims from the request
	JWT ContextKey = "jwt_claims"

	// NamespaceOverride stores the namespace override for the request
	NamespaceOverride ContextKey = "namespace_override"
)
