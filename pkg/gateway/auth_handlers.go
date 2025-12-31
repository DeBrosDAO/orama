package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
)

func (g *Gateway) whoamiHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	// Determine namespace (may be overridden by auth layer)
	ns := g.cfg.ClientNamespace
	if v := ctx.Value(ctxKeyNamespaceOverride); v != nil {
		if s, ok := v.(string); ok && s != "" {
			ns = s
		}
	}

	// Prefer JWT if present
	if v := ctx.Value(ctxKeyJWT); v != nil {
		if claims, ok := v.(*auth.JWTClaims); ok && claims != nil {
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
	if g.authService == nil {
		writeError(w, http.StatusServiceUnavailable, "auth service not initialized")
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

	nonce, err := g.authService.CreateNonce(r.Context(), req.Wallet, req.Purpose, req.Namespace)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"wallet":     req.Wallet,
		"namespace":  req.Namespace,
		"nonce":      nonce,
		"purpose":    req.Purpose,
		"expires_at": time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339Nano),
	})
}

func (g *Gateway) verifyHandler(w http.ResponseWriter, r *http.Request) {
	if g.authService == nil {
		writeError(w, http.StatusServiceUnavailable, "auth service not initialized")
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
		ChainType string `json:"chain_type"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if strings.TrimSpace(req.Wallet) == "" || strings.TrimSpace(req.Nonce) == "" || strings.TrimSpace(req.Signature) == "" {
		writeError(w, http.StatusBadRequest, "wallet, nonce and signature are required")
		return
	}

	ctx := r.Context()
	verified, err := g.authService.VerifySignature(ctx, req.Wallet, req.Nonce, req.Signature, req.ChainType)
	if err != nil || !verified {
		writeError(w, http.StatusUnauthorized, "signature verification failed")
		return
	}

	// Mark nonce used
	nsID, _ := g.authService.ResolveNamespaceID(ctx, req.Namespace)
	db := g.client.Database()
	_, _ = db.Query(client.WithInternalAuth(ctx), "UPDATE nonces SET used_at = datetime('now') WHERE namespace_id = ? AND wallet = ? AND nonce = ?", nsID, strings.ToLower(req.Wallet), req.Nonce)

	token, refresh, expUnix, err := g.authService.IssueTokens(ctx, req.Wallet, req.Namespace)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	apiKey, err := g.authService.GetOrCreateAPIKey(ctx, req.Wallet, req.Namespace)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":       token,
		"token_type":         "Bearer",
		"expires_in":         int(expUnix - time.Now().Unix()),
		"refresh_token":      refresh,
		"subject":            req.Wallet,
		"namespace":          req.Namespace,
		"api_key":            apiKey,
		"nonce":              req.Nonce,
		"signature_verified": true,
	})
}

func (g *Gateway) issueAPIKeyHandler(w http.ResponseWriter, r *http.Request) {
	if g.authService == nil {
		writeError(w, http.StatusServiceUnavailable, "auth service not initialized")
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
		ChainType string `json:"chain_type"`
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

	ctx := r.Context()
	verified, err := g.authService.VerifySignature(ctx, req.Wallet, req.Nonce, req.Signature, req.ChainType)
	if err != nil || !verified {
		writeError(w, http.StatusUnauthorized, "signature verification failed")
		return
	}

	// Mark nonce used
	nsID, _ := g.authService.ResolveNamespaceID(ctx, req.Namespace)
	db := g.client.Database()
	_, _ = db.Query(client.WithInternalAuth(ctx), "UPDATE nonces SET used_at = datetime('now') WHERE namespace_id = ? AND wallet = ? AND nonce = ?", nsID, strings.ToLower(req.Wallet), req.Nonce)

	apiKey, err := g.authService.GetOrCreateAPIKey(ctx, req.Wallet, req.Namespace)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"api_key":   apiKey,
		"namespace": req.Namespace,
		"plan": func() string {
			if strings.TrimSpace(req.Plan) == "" {
				return "free"
			}
			return req.Plan
		}(),
		"wallet": strings.ToLower(strings.TrimPrefix(strings.TrimPrefix(req.Wallet, "0x"), "0X")),
	})
}

// apiKeyToJWTHandler issues a short-lived JWT for use with the gateway from a valid API key.
// Requires Authorization header with API key (Bearer or ApiKey or X-API-Key header).
// Returns a JWT bound to the namespace derived from the API key record.
func (g *Gateway) apiKeyToJWTHandler(w http.ResponseWriter, r *http.Request) {
	if g.authService == nil {
		writeError(w, http.StatusServiceUnavailable, "auth service not initialized")
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
	}

	token, expUnix, err := g.authService.GenerateJWT(ns, key, 15*time.Minute)
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
	if g.authService == nil {
		writeError(w, http.StatusServiceUnavailable, "auth service not initialized")
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
		ChainType string `json:"chain_type"`
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

	ctx := r.Context()
	verified, err := g.authService.VerifySignature(ctx, req.Wallet, req.Nonce, req.Signature, req.ChainType)
	if err != nil || !verified {
		writeError(w, http.StatusUnauthorized, "signature verification failed")
		return
	}

	// Mark nonce used
	nsID, _ := g.authService.ResolveNamespaceID(ctx, req.Namespace)
	db := g.client.Database()
	_, _ = db.Query(client.WithInternalAuth(ctx), "UPDATE nonces SET used_at = datetime('now') WHERE namespace_id = ? AND wallet = ? AND nonce = ?", nsID, strings.ToLower(req.Wallet), req.Nonce)

	// In a real app we'd derive the public key from the signature, but for simplicity here
	// we just use a placeholder or expect it in the request if needed.
	// For Ethereum, we can recover it.
	publicKey := "recovered-pk"

	appID, err := g.authService.RegisterApp(ctx, req.Wallet, req.Namespace, req.Name, publicKey)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"client_id": appID,
		"app": map[string]any{
			"app_id":    appID,
			"name":      req.Name,
			"namespace": req.Namespace,
			"wallet":    strings.ToLower(req.Wallet),
		},
		"signature_verified": true,
	})
}

func (g *Gateway) refreshHandler(w http.ResponseWriter, r *http.Request) {
	if g.authService == nil {
		writeError(w, http.StatusServiceUnavailable, "auth service not initialized")
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

	token, subject, expUnix, err := g.authService.RefreshToken(r.Context(), req.RefreshToken, req.Namespace)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":  token,
		"token_type":    "Bearer",
		"expires_in":    int(expUnix - time.Now().Unix()),
		"refresh_token": req.RefreshToken,
		"subject":       subject,
		"namespace":     req.Namespace,
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
	if g.authService == nil {
		writeError(w, http.StatusServiceUnavailable, "auth service not initialized")
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

	ctx := r.Context()
	var subject string
	if req.All {
		if v := ctx.Value(ctxKeyJWT); v != nil {
			if claims, ok := v.(*auth.JWTClaims); ok && claims != nil {
				subject = strings.TrimSpace(claims.Sub)
			}
		}
		if subject == "" {
			writeError(w, http.StatusUnauthorized, "jwt required for all=true")
			return
		}
	}

	if err := g.authService.RevokeToken(ctx, req.Namespace, req.RefreshToken, req.All, subject); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (g *Gateway) simpleAPIKeyHandler(w http.ResponseWriter, r *http.Request) {
	if g.authService == nil {
		writeError(w, http.StatusServiceUnavailable, "auth service not initialized")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		Wallet    string `json:"wallet"`
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

	apiKey, err := g.authService.GetOrCreateAPIKey(r.Context(), req.Wallet, req.Namespace)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"api_key":   apiKey,
		"namespace": req.Namespace,
		"wallet":    strings.ToLower(strings.TrimPrefix(strings.TrimPrefix(req.Wallet, "0x"), "0X")),
		"created":   time.Now().Format(time.RFC3339),
	})
}
