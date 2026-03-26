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
