package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// sha256Hex returns the lowercase hex-encoded SHA-256 hash of the input string.
// Used to hash refresh tokens before storage — deterministic so we can hash on
// insert and hash on lookup without storing the raw token.
func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// HmacSHA256Hex computes HMAC-SHA256 of data with the given secret key and
// returns the result as a lowercase hex string. Used for API key hashing —
// fast and deterministic, allowing direct DB lookup by hash.
func HmacSHA256Hex(data, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(data))
	return hex.EncodeToString(mac.Sum(nil))
}
