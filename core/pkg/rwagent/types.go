// Package rwagent provides a Go client for the RootWallet agent daemon.
//
// The agent is a persistent daemon that holds vault keys in memory and serves
// operations to authorized apps over a Unix socket HTTP API. This SDK replaces
// all subprocess `rw` calls with direct HTTP communication.
package rwagent

// StatusResponse from GET /v1/status.
type StatusResponse struct {
	Version       string `json:"version"`
	Locked        bool   `json:"locked"`
	Uptime        int    `json:"uptime"`
	PID           int    `json:"pid"`
	ConnectedApps int    `json:"connectedApps"`
	// PendingUnlocks is how many requests are parked waiting for the wallet to
	// be unlocked.
	PendingUnlocks int `json:"pendingUnlocks"`
	// PendingApprovals is how many requests are waiting on an approval prompt
	// someone has to answer in the desktop app. The agent reports it
	// separately from PendingUnlocks; an older agent omits it (zero).
	PendingApprovals int `json:"pendingApprovals"`
}

// TouchResponse from POST /v1/touch.
type TouchResponse struct {
	// Locked is true when the wallet was already locked; a touch never unlocks.
	Locked bool `json:"locked"`
	// AutoLockInSeconds is how long the session now has before it auto-locks;
	// nil when locked or when the agent never auto-locks.
	AutoLockInSeconds *int `json:"autoLockInSeconds"`
	// PendingApprovals is how many requests are waiting on an approval prompt.
	PendingApprovals int `json:"pendingApprovals"`
}

// VaultSSHData from GET /v1/vault/ssh/:host/:user.
type VaultSSHData struct {
	PrivateKey string `json:"privateKey,omitempty"`
	PublicKey  string `json:"publicKey,omitempty"`
}

// VaultPasswordData from GET /v1/vault/password/:domain/:user.
type VaultPasswordData struct {
	Password string `json:"password"`
}

// WalletAddressData from GET /v1/wallet/address.
type WalletAddressData struct {
	Address string `json:"address"`
	Chain   string `json:"chain"`
}

// ReleaseKeyData from GET /v1/orama/release/key: the wallet's release public
// key, which signs TUF release metadata and nothing else.
type ReleaseKeyData struct {
	Purpose   string `json:"purpose"`
	KeyType   string `json:"keyType"`
	PublicKey string `json:"publicKey"`
	Path      string `json:"path"`
}

// WalletSignData from POST /v1/wallet/sign.
type WalletSignData struct {
	Signature string `json:"signature"`
}

// OramaTxSignature is POST /v1/orama/tx/sign's answer, decoded from base64.
type OramaTxSignature struct {
	// Signature is the 64-byte r||s, s low, over SHA-256 of the SignDoc: what
	// goes into TxRaw.signatures.
	Signature []byte
	// PubKey is the 33-byte compressed secp256k1 key that made it.
	PubKey []byte
	// Address is PubKey's ORAMA account address (orama1…).
	Address string
}

// oramaTxSignData is OramaTxSignature on the wire.
type oramaTxSignData struct {
	Signature string `json:"signature"`
	PubKey    string `json:"pubKey"`
	Address   string `json:"address"`
}

// OramaAccount is GET /v1/orama/account's answer, decoded from base64: the
// ORAMA account the agent signs transactions for.
type OramaAccount struct {
	// Address is the account's ORAMA address (orama1…).
	Address string
	// PubKey is the 33-byte compressed secp256k1 key of the account.
	PubKey []byte
}

// oramaAccountData is OramaAccount on the wire.
type oramaAccountData struct {
	Address   string `json:"address"`
	PublicKey string `json:"publicKey"`
}

// AppPermission represents an approved app in the permission database.
type AppPermission struct {
	BinaryHash   string                `json:"binaryHash"`
	BinaryPath   string                `json:"binaryPath"`
	Name         string                `json:"name"`
	FirstSeen    string                `json:"firstSeen"`
	LastUsed     string                `json:"lastUsed"`
	Capabilities []PermittedCapability `json:"capabilities"`
}

// PermittedCapability is a specific capability granted to an app.
type PermittedCapability struct {
	Capability string `json:"capability"`
	GrantedAt  string `json:"grantedAt"`
}

// apiResponse is the generic API response envelope.
type apiResponse[T any] struct {
	OK    bool   `json:"ok"`
	Data  T      `json:"data,omitempty"`
	Error string `json:"error,omitempty"`
	Code  string `json:"code,omitempty"`
}
