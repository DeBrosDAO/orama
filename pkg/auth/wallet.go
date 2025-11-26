package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

// WalletAuthResult represents the result of wallet authentication
type WalletAuthResult struct {
	APIKey       string `json:"api_key"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Namespace    string `json:"namespace"`
	Wallet       string `json:"wallet"`
	Plan         string `json:"plan,omitempty"`
	ExpiresAt    string `json:"expires_at,omitempty"`
}

// AuthServer handles the local HTTP server for receiving auth callbacks
type AuthServer struct {
	server   *http.Server
	listener net.Listener
	result   chan WalletAuthResult
	err      chan error
	mu       sync.Mutex
	done     bool
}

// PerformWalletAuthentication starts the complete wallet authentication flow
func PerformWalletAuthentication(gatewayURL string) (*Credentials, error) {
	fmt.Printf("🔐 Starting wallet authentication for gateway: %s\n", gatewayURL)

	// Start local callback server
	authServer, err := NewAuthServer()
	if err != nil {
		return nil, fmt.Errorf("failed to start auth server: %w", err)
	}
	defer authServer.Close()

	callbackURL := fmt.Sprintf("http://localhost:%d/callback", authServer.GetPort())
	fmt.Printf("📡 Authentication server started on port %d\n", authServer.GetPort())

	// Open browser to gateway auth page
	authURL := fmt.Sprintf("%s/v1/auth/login?callback=%s", gatewayURL, url.QueryEscape(callbackURL))
	fmt.Printf("🌐 Opening browser to: %s\n", authURL)

	if err := openBrowser(authURL); err != nil {
		fmt.Printf("⚠️  Failed to open browser automatically: %v\n", err)
		fmt.Printf("📋 Please manually open this URL in your browser:\n%s\n", authURL)
	}

	fmt.Println("⏳ Waiting for authentication to complete...")
	fmt.Println("💡 Complete the wallet signature in your browser, then return here.")

	// Wait for authentication result with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	select {
	case result := <-authServer.result:
		fmt.Println("✅ Authentication successful!")
		return convertAuthResult(result), nil
	case err := <-authServer.err:
		return nil, fmt.Errorf("authentication failed: %w", err)
	case <-ctx.Done():
		return nil, fmt.Errorf("authentication timed out after 5 minutes")
	}
}

// NewAuthServer creates a new authentication callback server
func NewAuthServer() (*AuthServer, error) {
	// Listen on random available port
	listener, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		return nil, fmt.Errorf("failed to create listener: %w", err)
	}

	authServer := &AuthServer{
		listener: listener,
		result:   make(chan WalletAuthResult, 1),
		err:      make(chan error, 1),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", authServer.handleCallback)
	mux.HandleFunc("/health", authServer.handleHealth)

	authServer.server = &http.Server{
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	// Start server in background
	go func() {
		if err := authServer.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			authServer.err <- fmt.Errorf("auth server error: %w", err)
		}
	}()

	return authServer, nil
}

// GetPort returns the port the server is listening on
func (as *AuthServer) GetPort() int {
	return as.listener.Addr().(*net.TCPAddr).Port
}

// Close shuts down the authentication server
func (as *AuthServer) Close() error {
	as.mu.Lock()
	defer as.mu.Unlock()

	if as.done {
		return nil
	}
	as.done = true

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return as.server.Shutdown(ctx)
}

// handleCallback processes the authentication callback from the gateway
func (as *AuthServer) handleCallback(w http.ResponseWriter, r *http.Request) {
	as.mu.Lock()
	if as.done {
		as.mu.Unlock()
		return
	}
	as.mu.Unlock()

	// Parse query parameters
	query := r.URL.Query()

	// Check for error
	if errMsg := query.Get("error"); errMsg != "" {
		as.err <- fmt.Errorf("authentication error: %s", errMsg)
		http.Error(w, "Authentication failed", http.StatusBadRequest)
		return
	}

	// Extract authentication result
	result := WalletAuthResult{
		APIKey:       query.Get("api_key"),
		RefreshToken: query.Get("refresh_token"),
		Namespace:    query.Get("namespace"),
		Wallet:       query.Get("wallet"),
		Plan:         query.Get("plan"),
		ExpiresAt:    query.Get("expires_at"),
	}

	// Validate required fields
	if result.APIKey == "" || result.Namespace == "" {
		as.err <- fmt.Errorf("incomplete authentication response: missing api_key or namespace")
		http.Error(w, "Incomplete authentication response", http.StatusBadRequest)
		return
	}

	// Send success response to browser
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `
<!DOCTYPE html>
<html>
<head>
    <title>Authentication Successful</title>
    <style>
        body { font-family: Arial, sans-serif; text-align: center; padding: 50px; background: #f5f5f5; }
        .container { background: white; padding: 30px; border-radius: 10px; box-shadow: 0 2px 10px rgba(0,0,0,0.1); max-width: 500px; margin: 0 auto; }
        .success { color: #4CAF50; font-size: 48px; margin-bottom: 20px; }
        .details { background: #f8f9fa; padding: 20px; border-radius: 5px; margin: 20px 0; text-align: left; }
        .key { font-family: monospace; background: #e9ecef; padding: 10px; border-radius: 3px; word-break: break-all; }
    </style>
</head>
<body>
    <div class="container">
        <div class="success">✅</div>
        <h1>Authentication Successful!</h1>
        <p>You have successfully authenticated with your wallet.</p>

        <div class="details">
            <h3>🔑 Your Credentials:</h3>
            <p><strong>API Key:</strong></p>
            <div class="key">%s</div>
            <p><strong>Namespace:</strong> %s</p>
            <p><strong>Wallet:</strong> %s</p>
            %s
        </div>

        <p>Your credentials have been saved securely to <code>~/.orama/credentials.json</code></p>
        <p><strong>You can now close this browser window and return to your terminal.</strong></p>
    </div>
</body>
</html>`,
		result.APIKey,
		result.Namespace,
		result.Wallet,
		func() string {
			if result.Plan != "" {
				return fmt.Sprintf("<p><strong>Plan:</strong> %s</p>", result.Plan)
			}
			return ""
		}(),
	)

	// Send result to waiting goroutine
	select {
	case as.result <- result:
		// Success
	default:
		// Channel full, ignore
	}
}

// handleHealth provides a simple health check endpoint
func (as *AuthServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"status": "ok",
		"server": "debros-auth-callback",
	})
}

// convertAuthResult converts WalletAuthResult to Credentials
func convertAuthResult(result WalletAuthResult) *Credentials {
	creds := &Credentials{
		APIKey:    result.APIKey,
		Namespace: result.Namespace,
		UserID:    result.Wallet,
		Wallet:    result.Wallet,
		IssuedAt:  time.Now(),
		Plan:      result.Plan,
	}

	// Set refresh token if provided
	if result.RefreshToken != "" {
		creds.RefreshToken = result.RefreshToken
	}

	// Parse expiration if provided
	if result.ExpiresAt != "" {
		if expTime, err := time.Parse(time.RFC3339, result.ExpiresAt); err == nil {
			creds.ExpiresAt = expTime
		}
	}

	return creds
}

// openBrowser opens the default browser to the specified URL
func openBrowser(url string) error {
	var cmd string
	var args []string

	switch runtime.GOOS {
	case "windows":
		cmd = "cmd"
		args = []string{"/c", "start"}
	case "darwin":
		cmd = "open"
	default: // "linux", "freebsd", "openbsd", "netbsd"
		cmd = "xdg-open"
	}
	args = append(args, url)

	return exec.Command(cmd, args...).Start()
}

// GenerateRandomString generates a cryptographically secure random string
func GenerateRandomString(length int) (string, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes)[:length], nil
}

// ValidateWalletAddress validates that a wallet address is properly formatted
func ValidateWalletAddress(address string) bool {
	// Remove 0x prefix if present
	addr := strings.TrimPrefix(strings.ToLower(address), "0x")

	// Check length (Ethereum addresses are 40 hex characters)
	if len(addr) != 40 {
		return false
	}

	// Check if all characters are hex
	_, err := hex.DecodeString(addr)
	return err == nil
}

// FormatWalletAddress formats a wallet address consistently
func FormatWalletAddress(address string) string {
	addr := strings.TrimPrefix(strings.ToLower(address), "0x")
	return "0x" + addr
}
