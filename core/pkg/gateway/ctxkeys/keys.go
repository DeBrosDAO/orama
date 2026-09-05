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

	// Grant holds the *auth.Grant the authorization middleware resolved for a
	// request's SIWE wallet JWT in the namespace it names. It is what the scope
	// gate turns into the caller's grant set, so an owner or admin reaches the
	// control plane and a runtime member does not.
	//
	// It used to be a bare true — "a wallet owner was confirmed" — which is why
	// everyone with access to a namespace was an admin: a boolean has one thing
	// to say. See pkg/gateway/auth/grants.go.
	Grant ContextKey = "namespace_grant"

	// Permissions holds the auth.PermissionSet this request's credential
	// amounts to, computed once by the scope middleware.
	//
	// It exists so that the gate and the handler ask the same question of the
	// same answer. The gate asks "does this reach the domain at all", before
	// the handler knows which object; the handler asks again with the object.
	// They used to be computed separately, from different inputs.
	Permissions ContextKey = "permissions"
)
