package middleware

import (
	"net/http"
	"strings"

	"github.com/debros/orama-website/invest-api/auth"
)

// RequireAuth validates JWT from Authorization header.
// Falls back to X-Wallet-Address/X-Wallet-Chain headers for backward compatibility.
func RequireAuth(jwtSecret string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Try JWT first
			authHeader := r.Header.Get("Authorization")
			if strings.HasPrefix(authHeader, "Bearer ") {
				tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
				claims, err := auth.ParseToken(tokenStr, jwtSecret)
				if err != nil {
					http.Error(w, `{"error":"invalid or expired token"}`, http.StatusUnauthorized)
					return
				}
				ctx := auth.WithWallet(r.Context(), claims.Wallet, claims.Chain)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			// Fallback: legacy header-based auth (remove after frontend migration)
			wallet := strings.TrimSpace(r.Header.Get("X-Wallet-Address"))
			chain := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Wallet-Chain")))
			if wallet != "" && (chain == "sol" || chain == "evm") {
				ctx := auth.WithWallet(r.Context(), wallet, chain)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			http.Error(w, `{"error":"authentication required"}`, http.StatusUnauthorized)
		})
	}
}
