package auth

// ChallengeRequest is the request body for challenge generation
type ChallengeRequest struct {
	Wallet    string `json:"wallet"`
	Purpose   string `json:"purpose"`
	Namespace string `json:"namespace"`
}

// VerifyRequest is the request body for signature verification
type VerifyRequest struct {
	Wallet    string `json:"wallet"`
	Nonce     string `json:"nonce"`
	Signature string `json:"signature"`
	Namespace string `json:"namespace"`
	ChainType string `json:"chain_type"`

	// DevicePublicKey / DeviceSignature are the OPTIONAL device assertion
	// (bugboard feat-384): an ed25519 signature over the same login challenge
	// the account signed, made with the device's own key. Presenting both binds
	// the issued token to that device and stamps an unforgeable device claim
	// the namespace's functions can authorize against.
	//
	// Optional by design. The CLI, the SDK and RootWallet-signed operator
	// logins cannot produce one, and making it mandatory would break every
	// existing client. A function that REQUIRES device attribution must deny
	// when the claim is absent — absence is not permission.
	DevicePublicKey string `json:"device_public_key,omitempty"`
	DeviceSignature string `json:"device_signature,omitempty"`
}

// APIKeyRequest is the request body for API key generation
type APIKeyRequest struct {
	Wallet    string `json:"wallet"`
	Nonce     string `json:"nonce"`
	Signature string `json:"signature"`
	Namespace string `json:"namespace"`
	ChainType string `json:"chain_type"`
	Plan      string `json:"plan"`
}

// SimpleAPIKeyRequest is the request body for simple API key generation (no signature)
type SimpleAPIKeyRequest struct {
	Wallet    string `json:"wallet"`
	Namespace string `json:"namespace"`
}

// RegisterRequest is the request body for app registration
type RegisterRequest struct {
	Wallet    string `json:"wallet"`
	Nonce     string `json:"nonce"`
	Signature string `json:"signature"`
	Namespace string `json:"namespace"`
	ChainType string `json:"chain_type"`
	Name      string `json:"name"`
}

// RefreshRequest is the request body for token refresh
type RefreshRequest struct {
	RefreshToken string `json:"refresh_token"`
	Namespace    string `json:"namespace"`
}

// LogoutRequest is the request body for logout/token revocation
type LogoutRequest struct {
	RefreshToken string `json:"refresh_token"`
	Namespace    string `json:"namespace"`
	All          bool   `json:"all"`
}
