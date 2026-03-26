package auth

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type LoginRequest struct {
	Wallet    string `json:"wallet"`
	Chain     string `json:"chain"`
	Message   string `json:"message"`
	Signature string `json:"signature"`
}

type LoginResponse struct {
	Token     string `json:"token"`
	Wallet    string `json:"wallet"`
	Chain     string `json:"chain"`
	ExpiresAt string `json:"expires_at"`
}

func LoginHandler(database *sql.DB, jwtSecret string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req LoginRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid request body")
			return
		}

		req.Chain = strings.ToLower(req.Chain)
		if req.Wallet == "" || req.Message == "" || req.Signature == "" {
			jsonError(w, http.StatusBadRequest, "wallet, message, and signature are required")
			return
		}
		if req.Chain != "sol" && req.Chain != "evm" {
			jsonError(w, http.StatusBadRequest, "chain must be 'sol' or 'evm'")
			return
		}

		// Validate message timestamp (anti-replay: within 5 minutes)
		if err := validateMessageTimestamp(req.Message); err != nil {
			jsonError(w, http.StatusBadRequest, fmt.Sprintf("message validation failed: %v", err))
			return
		}

		// Verify signature
		var verifyErr error
		if req.Chain == "sol" {
			verifyErr = VerifySolana(req.Wallet, req.Message, req.Signature)
		} else {
			verifyErr = VerifyEVM(req.Wallet, req.Message, req.Signature)
		}
		if verifyErr != nil {
			jsonError(w, http.StatusUnauthorized, fmt.Sprintf("signature verification failed: %v", verifyErr))
			return
		}

		// Upsert wallet
		database.Exec(
			`INSERT INTO wallets (wallet, chain) VALUES (?, ?)
			 ON CONFLICT(wallet) DO UPDATE SET last_seen = CURRENT_TIMESTAMP, chain = ?`,
			req.Wallet, req.Chain, req.Chain,
		)

		// Generate JWT
		token, expiresAt, err := GenerateToken(req.Wallet, req.Chain, jwtSecret)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "failed to generate token")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(LoginResponse{
			Token:     token,
			Wallet:    req.Wallet,
			Chain:     req.Chain,
			ExpiresAt: expiresAt.Format(time.RFC3339),
		})
	}
}

func validateMessageTimestamp(message string) error {
	// Extract timestamp from message format:
	// "...Timestamp: 2026-03-21T12:00:00.000Z"
	idx := strings.Index(message, "Timestamp: ")
	if idx == -1 {
		return fmt.Errorf("message does not contain a timestamp")
	}

	tsStr := strings.TrimSpace(message[idx+len("Timestamp: "):])
	// Handle potential trailing content
	if newline := strings.Index(tsStr, "\n"); newline != -1 {
		tsStr = tsStr[:newline]
	}

	ts, err := time.Parse(time.RFC3339Nano, tsStr)
	if err != nil {
		ts, err = time.Parse("2006-01-02T15:04:05.000Z", tsStr)
		if err != nil {
			return fmt.Errorf("invalid timestamp format: %s", tsStr)
		}
	}

	if time.Since(ts) > 5*time.Minute {
		return fmt.Errorf("message timestamp expired (older than 5 minutes)")
	}

	return nil
}

func jsonError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
