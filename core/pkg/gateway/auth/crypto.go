package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

// marshalClaims serializes additive JWT custom claims for storage alongside a
// refresh token (bugboard #548). Empty/nil → "" so the column stays NULL-ish
// and absent claims read back as nil.
func marshalClaims(m map[string]string) string {
	if len(m) == 0 {
		return ""
	}
	b, err := json.Marshal(m)
	if err != nil {
		return ""
	}
	return string(b)
}

// unmarshalClaims is the inverse of marshalClaims. An empty string or any
// malformed value yields nil (fail-soft — a corrupt claims blob must never
// break token rotation; the token simply rotates without custom claims).
func unmarshalClaims(s string) map[string]string {
	if s == "" {
		return nil
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil
	}
	if len(m) == 0 {
		return nil
	}
	return m
}
