package auth

// ChallengeRequest is the request body for challenge generation.
//
// ChainType decides which of the two message grammars the gateway renders —
// "Ethereum account" or "Solana account" in the header line — and therefore
// which signature scheme will verify the answer. Empty means ETH.
type ChallengeRequest struct {
	Wallet    string `json:"wallet"`
	Purpose   string `json:"purpose"`
	Namespace string `json:"namespace"`
	ChainType string `json:"chain_type"`
}

// VerifyRequest is the request body for signature verification.
//
// Message is the sign-in message the wallet signed, verbatim. Everything the
// gateway needs is inside it — the wallet, the nonce, the namespace, the domain
// it was signed for and when it expires — and reading any of those from beside
// it in the request body would mean acting on a field the user never saw and
// the signature does not cover.
type VerifyRequest struct {
	Message   string `json:"message"`
	Signature string `json:"signature"`
}

// APIKeyRequest is the request body for API key generation. See VerifyRequest
// for why the message is the whole credential.
type APIKeyRequest struct {
	Message   string `json:"message"`
	Signature string `json:"signature"`
	Plan      string `json:"plan"`
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
