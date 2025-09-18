package gateway

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/storage"
	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

func (g *Gateway) whoamiHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	// Determine namespace (may be overridden by auth layer)
	ns := g.cfg.ClientNamespace
	if v := ctx.Value(storage.CtxKeyNamespaceOverride); v != nil {
		if s, ok := v.(string); ok && s != "" {
			ns = s
		}
	}

	// Prefer JWT if present
	if v := ctx.Value(ctxKeyJWT); v != nil {
		if claims, ok := v.(*jwtClaims); ok && claims != nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"authenticated": true,
				"method":        "jwt",
				"subject":       claims.Sub,
				"issuer":        claims.Iss,
				"audience":      claims.Aud,
				"issued_at":     claims.Iat,
				"not_before":    claims.Nbf,
				"expires_at":    claims.Exp,
				"namespace":     ns,
			})
			return
		}
	}

	// Fallback: API key identity
	var key string
	if v := ctx.Value(ctxKeyAPIKey); v != nil {
		if s, ok := v.(string); ok {
			key = s
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"authenticated": key != "",
		"method":        "api_key",
		"api_key":       key,
		"namespace":     ns,
	})
}

func (g *Gateway) challengeHandler(w http.ResponseWriter, r *http.Request) {
	if g.client == nil {
		writeError(w, http.StatusServiceUnavailable, "client not initialized")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		Wallet    string `json:"wallet"`
		Purpose   string `json:"purpose"`
		Namespace string `json:"namespace"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if strings.TrimSpace(req.Wallet) == "" {
		writeError(w, http.StatusBadRequest, "wallet is required")
		return
	}
	ns := strings.TrimSpace(req.Namespace)
	if ns == "" {
		ns = strings.TrimSpace(g.cfg.ClientNamespace)
		if ns == "" {
			ns = "default"
		}
	}
	// Generate a URL-safe random nonce (32 bytes)
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate nonce")
		return
	}
	nonce := base64.RawURLEncoding.EncodeToString(buf)

	// Insert namespace if missing, fetch id
	ctx := r.Context()
	// Use internal context to bypass authentication for system operations
	internalCtx := client.WithInternalAuth(ctx)
	db := g.client.Database()
	if _, err := db.Query(internalCtx, "INSERT OR IGNORE INTO namespaces(name) VALUES (?)", ns); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	nres, err := db.Query(internalCtx, "SELECT id FROM namespaces WHERE name = ? LIMIT 1", ns)
	if err != nil || nres == nil || nres.Count == 0 || len(nres.Rows) == 0 || len(nres.Rows[0]) == 0 {
		writeError(w, http.StatusInternalServerError, "failed to resolve namespace")
		return
	}
	nsID := nres.Rows[0][0]

	// Store nonce with 5 minute expiry
	if _, err := db.Query(internalCtx,
		"INSERT INTO nonces(namespace_id, wallet, nonce, purpose, expires_at) VALUES (?, ?, ?, ?, datetime('now', '+5 minutes'))",
		nsID, req.Wallet, nonce, req.Purpose,
	); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"wallet":     req.Wallet,
		"namespace":  ns,
		"nonce":      nonce,
		"purpose":    req.Purpose,
		"expires_at": time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339Nano),
	})
}

func (g *Gateway) verifyHandler(w http.ResponseWriter, r *http.Request) {
	if g.client == nil {
		writeError(w, http.StatusServiceUnavailable, "client not initialized")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		Wallet    string `json:"wallet"`
		Nonce     string `json:"nonce"`
		Signature string `json:"signature"`
		Namespace string `json:"namespace"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if strings.TrimSpace(req.Wallet) == "" || strings.TrimSpace(req.Nonce) == "" || strings.TrimSpace(req.Signature) == "" {
		writeError(w, http.StatusBadRequest, "wallet, nonce and signature are required")
		return
	}
	ns := strings.TrimSpace(req.Namespace)
	if ns == "" {
		ns = strings.TrimSpace(g.cfg.ClientNamespace)
		if ns == "" {
			ns = "default"
		}
	}
	ctx := r.Context()
	// Use internal context to bypass authentication for system operations
	internalCtx := client.WithInternalAuth(ctx)
	db := g.client.Database()
	nsID, err := g.resolveNamespaceID(ctx, ns)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	q := "SELECT id FROM nonces WHERE namespace_id = ? AND wallet = ? AND nonce = ? AND used_at IS NULL AND (expires_at IS NULL OR expires_at > datetime('now')) LIMIT 1"
	nres, err := db.Query(internalCtx, q, nsID, req.Wallet, req.Nonce)
	if err != nil || nres == nil || nres.Count == 0 {
		writeError(w, http.StatusBadRequest, "invalid or expired nonce")
		return
	}
	nonceID := nres.Rows[0][0]

	// EVM personal_sign verification of the nonce
	// Hash: keccak256("\x19Ethereum Signed Message:\n" + len(nonce) + nonce)
	msg := []byte(req.Nonce)
	prefix := []byte("\x19Ethereum Signed Message:\n" + strconv.Itoa(len(msg)))
	hash := ethcrypto.Keccak256(prefix, msg)

	// Decode signature (expects 65-byte r||s||v, hex with optional 0x)
	sigHex := strings.TrimSpace(req.Signature)
	if strings.HasPrefix(sigHex, "0x") || strings.HasPrefix(sigHex, "0X") {
		sigHex = sigHex[2:]
	}
	sig, err := hex.DecodeString(sigHex)
	if err != nil || len(sig) != 65 {
		writeError(w, http.StatusBadRequest, "invalid signature format")
		return
	}
	// Normalize V to 0/1 as expected by geth
	if sig[64] >= 27 {
		sig[64] -= 27
	}
	pub, err := ethcrypto.SigToPub(hash, sig)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "signature recovery failed")
		return
	}
	addr := ethcrypto.PubkeyToAddress(*pub).Hex()
	want := strings.ToLower(strings.TrimPrefix(strings.TrimPrefix(req.Wallet, "0x"), "0X"))
	got := strings.ToLower(strings.TrimPrefix(strings.TrimPrefix(addr, "0x"), "0X"))
	if got != want {
		writeError(w, http.StatusUnauthorized, "signature does not match wallet")
		return
	}

	// Mark nonce used now (after successful verification)
	if _, err := db.Query(internalCtx, "UPDATE nonces SET used_at = datetime('now') WHERE id = ?", nonceID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if g.signingKey == nil {
		writeError(w, http.StatusServiceUnavailable, "signing key unavailable")
		return
	}
	// Issue access token (15m) and a refresh token (30d)
	token, expUnix, err := g.generateJWT(ns, req.Wallet, 15*time.Minute)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// create refresh token
	rbuf := make([]byte, 32)
	if _, err := rand.Read(rbuf); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate refresh token")
		return
	}
	refresh := base64.RawURLEncoding.EncodeToString(rbuf)
	if _, err := db.Query(internalCtx, "INSERT INTO refresh_tokens(namespace_id, subject, token, audience, expires_at) VALUES (?, ?, ?, ?, datetime('now', '+30 days'))", nsID, req.Wallet, refresh, "gateway"); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":       token,
		"token_type":         "Bearer",
		"expires_in":         int(expUnix - time.Now().Unix()),
		"refresh_token":      refresh,
		"subject":            req.Wallet,
		"namespace":          ns,
		"nonce":              req.Nonce,
		"signature_verified": true,
	})
}

// issueAPIKeyHandler creates or returns an API key for a verified wallet in a namespace.
// Requires: POST { wallet, nonce, signature, namespace }
// Behavior:
//   - Validates nonce and signature like verifyHandler
//   - Ensures namespace exists
//   - If an API key already exists for (namespace, wallet), returns it; else creates one
//   - Records namespace ownership mapping for the wallet and api_key
func (g *Gateway) issueAPIKeyHandler(w http.ResponseWriter, r *http.Request) {
	if g.client == nil {
		writeError(w, http.StatusServiceUnavailable, "client not initialized")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		Wallet    string `json:"wallet"`
		Nonce     string `json:"nonce"`
		Signature string `json:"signature"`
		Namespace string `json:"namespace"`
		Plan      string `json:"plan"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if strings.TrimSpace(req.Wallet) == "" || strings.TrimSpace(req.Nonce) == "" || strings.TrimSpace(req.Signature) == "" {
		writeError(w, http.StatusBadRequest, "wallet, nonce and signature are required")
		return
	}
	ns := strings.TrimSpace(req.Namespace)
	if ns == "" {
		ns = strings.TrimSpace(g.cfg.ClientNamespace)
		if ns == "" {
			ns = "default"
		}
	}
	ctx := r.Context()
	// Use internal context to bypass authentication for system operations
	internalCtx := client.WithInternalAuth(ctx)
	db := g.client.Database()
	// Resolve namespace id
	nsID, err := g.resolveNamespaceID(ctx, ns)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Validate nonce exists and not used/expired
	q := "SELECT id FROM nonces WHERE namespace_id = ? AND wallet = ? AND nonce = ? AND used_at IS NULL AND (expires_at IS NULL OR expires_at > datetime('now')) LIMIT 1"
	nres, err := db.Query(internalCtx, q, nsID, req.Wallet, req.Nonce)
	if err != nil || nres == nil || nres.Count == 0 {
		writeError(w, http.StatusBadRequest, "invalid or expired nonce")
		return
	}
	nonceID := nres.Rows[0][0]
	// Verify signature like verifyHandler
	msg := []byte(req.Nonce)
	prefix := []byte("\x19Ethereum Signed Message:\n" + strconv.Itoa(len(msg)))
	hash := ethcrypto.Keccak256(prefix, msg)
	sigHex := strings.TrimSpace(req.Signature)
	if strings.HasPrefix(sigHex, "0x") || strings.HasPrefix(sigHex, "0X") {
		sigHex = sigHex[2:]
	}
	sig, err := hex.DecodeString(sigHex)
	if err != nil || len(sig) != 65 {
		writeError(w, http.StatusBadRequest, "invalid signature format")
		return
	}
	if sig[64] >= 27 {
		sig[64] -= 27
	}
	pub, err := ethcrypto.SigToPub(hash, sig)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "signature recovery failed")
		return
	}
	addr := ethcrypto.PubkeyToAddress(*pub).Hex()
	want := strings.ToLower(strings.TrimPrefix(strings.TrimPrefix(req.Wallet, "0x"), "0X"))
	got := strings.ToLower(strings.TrimPrefix(strings.TrimPrefix(addr, "0x"), "0X"))
	if got != want {
		writeError(w, http.StatusUnauthorized, "signature does not match wallet")
		return
	}
	// Mark nonce used
	if _, err := db.Query(internalCtx, "UPDATE nonces SET used_at = datetime('now') WHERE id = ?", nonceID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Check if api key exists for (namespace, wallet) via linkage table
	var apiKey string
	r1, err := db.Query(internalCtx, "SELECT api_keys.key FROM wallet_api_keys JOIN api_keys ON wallet_api_keys.api_key_id = api_keys.id WHERE wallet_api_keys.namespace_id = ? AND LOWER(wallet_api_keys.wallet) = LOWER(?) LIMIT 1", nsID, req.Wallet)
	if err == nil && r1 != nil && r1.Count > 0 && len(r1.Rows) > 0 && len(r1.Rows[0]) > 0 {
		if s, ok := r1.Rows[0][0].(string); ok {
			apiKey = s
		} else {
			b, _ := json.Marshal(r1.Rows[0][0])
			_ = json.Unmarshal(b, &apiKey)
		}
	}
	if strings.TrimSpace(apiKey) == "" {
		// Create new API key with format ak_<random>:<namespace>
		buf := make([]byte, 18)
		if _, err := rand.Read(buf); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to generate api key")
			return
		}
		apiKey = "ak_" + base64.RawURLEncoding.EncodeToString(buf) + ":" + ns
		if _, err := db.Query(internalCtx, "INSERT INTO api_keys(key, name, namespace_id) VALUES (?, ?, ?)", apiKey, "", nsID); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		// Create linkage
		// Find api_key id
		rid, err := db.Query(internalCtx, "SELECT id FROM api_keys WHERE key = ? LIMIT 1", apiKey)
		if err == nil && rid != nil && rid.Count > 0 && len(rid.Rows) > 0 && len(rid.Rows[0]) > 0 {
			apiKeyID := rid.Rows[0][0]
			_, _ = db.Query(internalCtx, "INSERT OR IGNORE INTO wallet_api_keys(namespace_id, wallet, api_key_id) VALUES (?, ?, ?)", nsID, strings.ToLower(req.Wallet), apiKeyID)
		}
	}
	// Record ownerships (best-effort)
	_, _ = db.Query(internalCtx, "INSERT OR IGNORE INTO namespace_ownership(namespace_id, owner_type, owner_id) VALUES (?, 'api_key', ?)", nsID, apiKey)
	_, _ = db.Query(internalCtx, "INSERT OR IGNORE INTO namespace_ownership(namespace_id, owner_type, owner_id) VALUES (?, 'wallet', ?)", nsID, req.Wallet)

	writeJSON(w, http.StatusOK, map[string]any{
		"api_key":   apiKey,
		"namespace": ns,
		"plan": func() string {
			if strings.TrimSpace(req.Plan) == "" {
				return "free"
			} else {
				return req.Plan
			}
		}(),
		"wallet": strings.ToLower(strings.TrimPrefix(strings.TrimPrefix(req.Wallet, "0x"), "0X")),
	})
}

// apiKeyToJWTHandler issues a short-lived JWT for use with the gateway from a valid API key.
// Requires Authorization header with API key (Bearer or ApiKey or X-API-Key header).
// Returns a JWT bound to the namespace derived from the API key record.
func (g *Gateway) apiKeyToJWTHandler(w http.ResponseWriter, r *http.Request) {
	if g.client == nil {
		writeError(w, http.StatusServiceUnavailable, "client not initialized")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	key := extractAPIKey(r)
	if strings.TrimSpace(key) == "" {
		writeError(w, http.StatusUnauthorized, "missing API key")
		return
	}
	// Validate and get namespace
	db := g.client.Database()
	ctx := r.Context()
	// Use internal context to bypass authentication for system operations
	internalCtx := client.WithInternalAuth(ctx)
	q := "SELECT namespaces.name FROM api_keys JOIN namespaces ON api_keys.namespace_id = namespaces.id WHERE api_keys.key = ? LIMIT 1"
	res, err := db.Query(internalCtx, q, key)
	if err != nil || res == nil || res.Count == 0 || len(res.Rows) == 0 || len(res.Rows[0]) == 0 {
		writeError(w, http.StatusUnauthorized, "invalid API key")
		return
	}
	var ns string
	if s, ok := res.Rows[0][0].(string); ok {
		ns = s
	} else {
		b, _ := json.Marshal(res.Rows[0][0])
		_ = json.Unmarshal(b, &ns)
	}
	ns = strings.TrimSpace(ns)
	if ns == "" {
		writeError(w, http.StatusUnauthorized, "invalid API key")
		return
	}
	if g.signingKey == nil {
		writeError(w, http.StatusServiceUnavailable, "signing key unavailable")
		return
	}
	// Subject is the API key string for now
	token, expUnix, err := g.generateJWT(ns, key, 15*time.Minute)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": token,
		"token_type":   "Bearer",
		"expires_in":   int(expUnix - time.Now().Unix()),
		"namespace":    ns,
	})
}

func (g *Gateway) registerHandler(w http.ResponseWriter, r *http.Request) {
	if g.client == nil {
		writeError(w, http.StatusServiceUnavailable, "client not initialized")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		Wallet    string `json:"wallet"`
		Nonce     string `json:"nonce"`
		Signature string `json:"signature"`
		Namespace string `json:"namespace"`
		Name      string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if strings.TrimSpace(req.Wallet) == "" || strings.TrimSpace(req.Nonce) == "" || strings.TrimSpace(req.Signature) == "" {
		writeError(w, http.StatusBadRequest, "wallet, nonce and signature are required")
		return
	}
	ns := strings.TrimSpace(req.Namespace)
	if ns == "" {
		ns = strings.TrimSpace(g.cfg.ClientNamespace)
		if ns == "" {
			ns = "default"
		}
	}
	ctx := r.Context()
	// Use internal context to bypass authentication for system operations
	internalCtx := client.WithInternalAuth(ctx)
	db := g.client.Database()
	nsID, err := g.resolveNamespaceID(ctx, ns)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Validate nonce
	q := "SELECT id FROM nonces WHERE namespace_id = ? AND wallet = ? AND nonce = ? AND used_at IS NULL AND (expires_at IS NULL OR expires_at > datetime('now')) LIMIT 1"
	nres, err := db.Query(internalCtx, q, nsID, req.Wallet, req.Nonce)
	if err != nil || nres == nil || nres.Count == 0 || len(nres.Rows) == 0 || len(nres.Rows[0]) == 0 {
		writeError(w, http.StatusBadRequest, "invalid or expired nonce")
		return
	}
	nonceID := nres.Rows[0][0]

	// EVM personal_sign verification of the nonce
	msg := []byte(req.Nonce)
	prefix := []byte("\x19Ethereum Signed Message:\n" + strconv.Itoa(len(msg)))
	hash := ethcrypto.Keccak256(prefix, msg)

	// Decode signature (expects 65-byte r||s||v, hex with optional 0x)
	sigHex := strings.TrimSpace(req.Signature)
	if strings.HasPrefix(sigHex, "0x") || strings.HasPrefix(sigHex, "0X") {
		sigHex = sigHex[2:]
	}
	sig, err := hex.DecodeString(sigHex)
	if err != nil || len(sig) != 65 {
		writeError(w, http.StatusBadRequest, "invalid signature format")
		return
	}
	// Normalize V to 0/1 as expected by geth
	if sig[64] >= 27 {
		sig[64] -= 27
	}
	pub, err := ethcrypto.SigToPub(hash, sig)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "signature recovery failed")
		return
	}
	addr := ethcrypto.PubkeyToAddress(*pub).Hex()
	want := strings.ToLower(strings.TrimPrefix(strings.TrimPrefix(req.Wallet, "0x"), "0X"))
	got := strings.ToLower(strings.TrimPrefix(strings.TrimPrefix(addr, "0x"), "0X"))
	if got != want {
		writeError(w, http.StatusUnauthorized, "signature does not match wallet")
		return
	}

	// Mark nonce used now (after successful verification)
	if _, err := db.Query(internalCtx, "UPDATE nonces SET used_at = datetime('now') WHERE id = ?", nonceID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Derive public key (uncompressed) hex
	pubBytes := ethcrypto.FromECDSAPub(pub)
	pubHex := "0x" + hex.EncodeToString(pubBytes)

	// Generate client app_id
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate app id")
		return
	}
	appID := "app_" + base64.RawURLEncoding.EncodeToString(buf)

	// Persist app
	if _, err := db.Query(internalCtx, "INSERT INTO apps(namespace_id, app_id, name, public_key) VALUES (?, ?, ?, ?)", nsID, appID, req.Name, pubHex); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Record namespace ownership by wallet (best-effort)
	_, _ = db.Query(internalCtx, "INSERT OR IGNORE INTO namespace_ownership(namespace_id, owner_type, owner_id) VALUES (?, ?, ?)", nsID, "wallet", req.Wallet)

	writeJSON(w, http.StatusCreated, map[string]any{
		"client_id": appID,
		"app": map[string]any{
			"app_id":     appID,
			"name":       req.Name,
			"public_key": pubHex,
			"namespace":  ns,
			"wallet":     strings.ToLower(strings.TrimPrefix(strings.TrimPrefix(req.Wallet, "0x"), "0X")),
		},
		"signature_verified": true,
	})
}

func (g *Gateway) refreshHandler(w http.ResponseWriter, r *http.Request) {
	if g.client == nil {
		writeError(w, http.StatusServiceUnavailable, "client not initialized")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		RefreshToken string `json:"refresh_token"`
		Namespace    string `json:"namespace"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if strings.TrimSpace(req.RefreshToken) == "" {
		writeError(w, http.StatusBadRequest, "refresh_token is required")
		return
	}
	ns := strings.TrimSpace(req.Namespace)
	if ns == "" {
		ns = strings.TrimSpace(g.cfg.ClientNamespace)
		if ns == "" {
			ns = "default"
		}
	}
	ctx := r.Context()
	// Use internal context to bypass authentication for system operations
	internalCtx := client.WithInternalAuth(ctx)
	db := g.client.Database()
	nsID, err := g.resolveNamespaceID(ctx, ns)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	q := "SELECT subject FROM refresh_tokens WHERE namespace_id = ? AND token = ? AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > datetime('now')) LIMIT 1"
	rres, err := db.Query(internalCtx, q, nsID, req.RefreshToken)
	if err != nil || rres == nil || rres.Count == 0 {
		writeError(w, http.StatusUnauthorized, "invalid or expired refresh token")
		return
	}
	subject := ""
	if len(rres.Rows) > 0 && len(rres.Rows[0]) > 0 {
		if s, ok := rres.Rows[0][0].(string); ok {
			subject = s
		} else {
			// fallback: format via json
			b, _ := json.Marshal(rres.Rows[0][0])
			_ = json.Unmarshal(b, &subject)
		}
	}
	if g.signingKey == nil {
		writeError(w, http.StatusServiceUnavailable, "signing key unavailable")
		return
	}
	token, expUnix, err := g.generateJWT(ns, subject, 15*time.Minute)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":  token,
		"token_type":    "Bearer",
		"expires_in":    int(expUnix - time.Now().Unix()),
		"refresh_token": req.RefreshToken,
		"subject":       subject,
		"namespace":     ns,
	})
}

// loginPageHandler serves the wallet authentication login page
func (g *Gateway) loginPageHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	callbackURL := r.URL.Query().Get("callback")
	if callbackURL == "" {
		writeError(w, http.StatusBadRequest, "callback parameter is required")
		return
	}

	// Get default namespace
	ns := strings.TrimSpace(g.cfg.ClientNamespace)
	if ns == "" {
		ns = "default"
	}

	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusOK)

	html := fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>DeBros Network - Wallet Authentication</title>
    <style>
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
            background: linear-gradient(135deg, #667eea 0%%, #764ba2 100%%);
            margin: 0;
            padding: 20px;
            min-height: 100vh;
            display: flex;
            align-items: center;
            justify-content: center;
        }
        .container {
            background: white;
            border-radius: 16px;
            box-shadow: 0 20px 60px rgba(0, 0, 0, 0.1);
            padding: 40px;
            max-width: 500px;
            width: 100%%;
            text-align: center;
        }
        .logo {
            font-size: 32px;
            font-weight: bold;
            color: #667eea;
            margin-bottom: 10px;
        }
        .subtitle {
            color: #666;
            margin-bottom: 30px;
        }
        .step {
            background: #f8f9fa;
            border-radius: 8px;
            padding: 20px;
            margin: 20px 0;
            text-align: left;
        }
        .step-number {
            background: #667eea;
            color: white;
            border-radius: 50%%;
            width: 24px;
            height: 24px;
            display: inline-flex;
            align-items: center;
            justify-content: center;
            font-weight: bold;
            margin-right: 10px;
        }
        button {
            background: #667eea;
            color: white;
            border: none;
            border-radius: 8px;
            padding: 12px 24px;
            font-size: 16px;
            font-weight: 600;
            cursor: pointer;
            transition: all 0.2s;
            margin: 10px;
        }
        button:hover {
            background: #5a67d8;
            transform: translateY(-1px);
        }
        button:disabled {
            background: #cbd5e0;
            cursor: not-allowed;
            transform: none;
        }
        .error {
            background: #fed7d7;
            color: #e53e3e;
            padding: 12px;
            border-radius: 8px;
            margin: 20px 0;
            display: none;
        }
        .success {
            background: #c6f6d5;
            color: #2f855a;
            padding: 12px;
            border-radius: 8px;
            margin: 20px 0;
            display: none;
        }
        .loading {
            display: none;
            margin: 20px 0;
        }
        .spinner {
            border: 3px solid #f3f3f3;
            border-top: 3px solid #667eea;
            border-radius: 50%%;
            width: 30px;
            height: 30px;
            animation: spin 1s linear infinite;
            margin: 0 auto;
        }
        @keyframes spin {
            0%% { transform: rotate(0deg); }
            100%% { transform: rotate(360deg); }
        }
        .namespace-info {
            background: #e6fffa;
            border: 1px solid #81e6d9;
            border-radius: 8px;
            padding: 15px;
            margin: 20px 0;
        }
        .code {
            font-family: 'Monaco', 'Menlo', monospace;
            background: #f7fafc;
            padding: 4px 8px;
            border-radius: 4px;
            font-size: 14px;
        }
    </style>
</head>
<body>
    <div class="container">
        <div class="logo">🌐 DeBros Network</div>
        <p class="subtitle">Secure Wallet Authentication</p>

        <div class="namespace-info">
            <strong>📁 Namespace:</strong> <span class="code">%s</span>
        </div>

        <div class="step">
            <div><span class="step-number">1</span><strong>Connect Your Wallet</strong></div>
            <p>Click the button below to connect your Ethereum wallet (MetaMask, WalletConnect, etc.)</p>
        </div>

        <div class="step">
            <div><span class="step-number">2</span><strong>Sign Authentication Message</strong></div>
            <p>Your wallet will prompt you to sign a message to prove your identity. This is free and secure.</p>
        </div>

        <div class="step">
            <div><span class="step-number">3</span><strong>Get Your API Key</strong></div>
            <p>After signing, you'll receive an API key to access the DeBros Network.</p>
        </div>

        <div class="error" id="error"></div>
        <div class="success" id="success"></div>

        <div class="loading" id="loading">
            <div class="spinner"></div>
            <p>Processing authentication...</p>
        </div>

        <button onclick="connectWallet()" id="connectBtn">🔗 Connect Wallet</button>
        <button onclick="window.close()" style="background: #718096;">❌ Cancel</button>
    </div>

    <script>
        const callbackURL = '%s';
        const namespace = '%s';
        let walletAddress = null;

        async function connectWallet() {
            const btn = document.getElementById('connectBtn');
            const loading = document.getElementById('loading');
            const error = document.getElementById('error');
            const success = document.getElementById('success');

            try {
                btn.disabled = true;
                loading.style.display = 'block';
                error.style.display = 'none';
                success.style.display = 'none';

                // Check if MetaMask is available
                if (typeof window.ethereum === 'undefined') {
                    throw new Error('Please install MetaMask or another Ethereum wallet');
                }

                // Request account access
                const accounts = await window.ethereum.request({
                    method: 'eth_requestAccounts'
                });

                if (accounts.length === 0) {
                    throw new Error('No wallet accounts found');
                }

                walletAddress = accounts[0];
                console.log('Connected to wallet:', walletAddress);

                // Step 1: Get challenge nonce
                const challengeResponse = await fetch('/v1/auth/challenge', {
                    method: 'POST',
                    headers: {
                        'Content-Type': 'application/json',
                    },
                    body: JSON.stringify({
                        wallet: walletAddress,
                        purpose: 'api_key_generation',
                        namespace: namespace
                    })
                });

                if (!challengeResponse.ok) {
                    const errorData = await challengeResponse.json();
                    throw new Error(errorData.error || 'Failed to get challenge');
                }

                const challengeData = await challengeResponse.json();
                const nonce = challengeData.nonce;

                console.log('Received challenge nonce:', nonce);

                // Step 2: Sign the nonce
                const signature = await window.ethereum.request({
                    method: 'personal_sign',
                    params: [nonce, walletAddress]
                });

                console.log('Signature obtained:', signature);

                // Step 3: Get API key
                const apiKeyResponse = await fetch('/v1/auth/api-key', {
                    method: 'POST',
                    headers: {
                        'Content-Type': 'application/json',
                    },
                    body: JSON.stringify({
                        wallet: walletAddress,
                        nonce: nonce,
                        signature: signature,
                        namespace: namespace
                    })
                });

                if (!apiKeyResponse.ok) {
                    const errorData = await apiKeyResponse.json();
                    throw new Error(errorData.error || 'Failed to get API key');
                }

                const apiKeyData = await apiKeyResponse.json();
                console.log('API key received:', apiKeyData);

                loading.style.display = 'none';
                success.innerHTML = '✅ Authentication successful! Redirecting...';
                success.style.display = 'block';

                // Redirect to callback URL with credentials
                const params = new URLSearchParams({
                    api_key: apiKeyData.api_key,
                    namespace: apiKeyData.namespace,
                    wallet: apiKeyData.wallet,
                    plan: apiKeyData.plan || 'free'
                });

                const redirectURL = callbackURL + '?' + params.toString();
                console.log('Redirecting to:', redirectURL);

                setTimeout(() => {
                    window.location.href = redirectURL;
                }, 1500);

            } catch (err) {
                console.error('Authentication error:', err);
                loading.style.display = 'none';
                error.innerHTML = '❌ ' + err.message;
                error.style.display = 'block';
                btn.disabled = false;
            }
        }

        // Auto-detect if wallet is already connected
        window.addEventListener('load', async () => {
            if (typeof window.ethereum !== 'undefined') {
                try {
                    const accounts = await window.ethereum.request({ method: 'eth_accounts' });
                    if (accounts.length > 0) {
                        const btn = document.getElementById('connectBtn');
                        btn.innerHTML = '🔗 Continue with ' + accounts[0].slice(0, 6) + '...' + accounts[0].slice(-4);
                    }
                } catch (err) {
                    console.log('Could not get accounts:', err);
                }
            }
        });
    </script>
</body>
</html>`, ns, callbackURL, ns)

	fmt.Fprint(w, html)
}

// logoutHandler revokes refresh tokens. If a refresh_token is provided, it will
// be revoked. If all=true is provided (and the request is authenticated via JWT),
// all tokens for the JWT subject within the namespace are revoked.
func (g *Gateway) logoutHandler(w http.ResponseWriter, r *http.Request) {
	if g.client == nil {
		writeError(w, http.StatusServiceUnavailable, "client not initialized")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		RefreshToken string `json:"refresh_token"`
		Namespace    string `json:"namespace"`
		All          bool   `json:"all"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	ns := strings.TrimSpace(req.Namespace)
	if ns == "" {
		ns = strings.TrimSpace(g.cfg.ClientNamespace)
		if ns == "" {
			ns = "default"
		}
	}
	ctx := r.Context()
	// Use internal context to bypass authentication for system operations
	internalCtx := client.WithInternalAuth(ctx)
	db := g.client.Database()
	nsID, err := g.resolveNamespaceID(ctx, ns)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if strings.TrimSpace(req.RefreshToken) != "" {
		// Revoke specific token
		if _, err := db.Query(internalCtx, "UPDATE refresh_tokens SET revoked_at = datetime('now') WHERE namespace_id = ? AND token = ? AND revoked_at IS NULL", nsID, req.RefreshToken); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "revoked": 1})
		return
	}

	if req.All {
		// Require JWT to identify subject
		var subject string
		if v := ctx.Value(ctxKeyJWT); v != nil {
			if claims, ok := v.(*jwtClaims); ok && claims != nil {
				subject = strings.TrimSpace(claims.Sub)
			}
		}
		if subject == "" {
			writeError(w, http.StatusUnauthorized, "jwt required for all=true")
			return
		}
		if _, err := db.Query(internalCtx, "UPDATE refresh_tokens SET revoked_at = datetime('now') WHERE namespace_id = ? AND subject = ? AND revoked_at IS NULL", nsID, subject); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "revoked": "all"})
		return
	}

	writeError(w, http.StatusBadRequest, "nothing to revoke: provide refresh_token or all=true")
}
