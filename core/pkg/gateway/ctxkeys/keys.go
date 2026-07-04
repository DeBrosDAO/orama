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

	// Scopes stores the auth.ScopeSet resolved for an API-key-authenticated
	// request (bugboard #148). Set by the auth middleware after key lookup.
	Scopes ContextKey = "api_key_scopes"

	// OwnerConfirmed is set to true by the authorization middleware when the
	// request's SIWE wallet JWT was verified to own the namespace. It lets the
	// scope gate grant admin to the namespace owner acting via a wallet JWT,
	// without granting admin to every authenticated user.
	OwnerConfirmed ContextKey = "owner_confirmed"
)
