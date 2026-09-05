package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash/crc32"
	"math/big"
	"strings"
)

// What an API key looks like, and why it looks like that.
//
// It used to be `ak_<24 base64url chars>:<namespace>`. Three problems with that
// string, none of them about the randomness in it:
//
//   - It carried the namespace. A key pasted into an issue, a log line or a
//     support ticket published which tenant it belonged to, before anybody had
//     decided whether the key itself was still live.
//   - It had no checksum, so nothing could tell an Orama key from any other
//     `ak_`-prefixed string without asking the gateway. That rules out
//     secret-scanning partnerships, which work by recognising a leaked
//     credential in a public commit *offline*, and it means a typo produces a
//     database lookup rather than an immediate "that is not a key".
//   - `ak_` is not distinctive. `orama_` is.
//
// The shape is `orama_<type>_<payload>_<checksum>`, all base62. The type is a
// label for whoever finds the string — it says how bad the leak is — and is
// *not* what decides the key's authority: the scopes column on the row is, and
// nothing here is consulted when a request is authorised.

const (
	// KeyPrefix is what every key starts with. It is the string a secret
	// scanner matches on, so it is deliberately unlike anything else.
	KeyPrefix = "orama"

	// KeyTypeService labels a key holding the control plane.
	KeyTypeService KeyType = "sk"
	// KeyTypeRuntime labels a key holding only the data plane.
	KeyTypeRuntime KeyType = "rk"

	// keyPayloadBytes is how much randomness a key carries. 24 bytes is 192
	// bits, which is past the point where the number matters.
	keyPayloadBytes = 24

	base62Alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
)

// KeyType is the label in the middle of a key.
type KeyType string

// keyTypes are the labels a key may carry.
var keyTypes = map[KeyType]string{
	KeyTypeService: "service key (control plane)",
	KeyTypeRuntime: "runtime key (data plane)",
}

// ErrNotAKey says a string is not an Orama key, and why. It is returned before
// any database is touched, which is the point of the checksum.
type ErrNotAKey struct{ Reason string }

func (e *ErrNotAKey) Error() string { return "not an Orama API key: " + e.Reason }

// KeyTypeFor is the label a key with these grants carries.
//
// It follows the grant set rather than being chosen: a key that holds admin is
// a control-plane credential whatever it is called, and the label exists so
// that whoever finds the string can tell.
func KeyTypeFor(storedScopes string) KeyType {
	if ScopesFromStored(storedScopes).IsAdmin() {
		return KeyTypeService
	}
	return KeyTypeRuntime
}

// NewKey mints a key string. The caller stores its hash, never this.
func NewKey(keyType KeyType) (string, error) {
	if _, ok := keyTypes[keyType]; !ok {
		return "", fmt.Errorf("unknown key type %q", keyType)
	}
	buf := make([]byte, keyPayloadBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("failed to generate api key: %w", err)
	}
	body := KeyPrefix + "_" + string(keyType) + "_" + base62Encode(new(big.Int).SetBytes(buf))
	return body + "_" + checksumOf(body), nil
}

// ParseKey validates a key's shape and checksum without touching a database,
// and returns the label it carries.
//
// A caller does not have to run this — the authoritative check is that the
// key's hash is on a live row — but running it first turns a typo into an
// immediate refusal instead of a query, and it is what lets anything outside
// this codebase recognise a leaked key.
func ParseKey(raw string) (KeyType, error) {
	raw = strings.TrimSpace(raw)
	parts := strings.Split(raw, "_")
	if len(parts) != 4 || parts[0] != KeyPrefix {
		return "", &ErrNotAKey{Reason: "the shape is " + KeyPrefix + "_<type>_<payload>_<checksum>"}
	}

	keyType := KeyType(parts[1])
	if _, ok := keyTypes[keyType]; !ok {
		return "", &ErrNotAKey{Reason: fmt.Sprintf("%q is not a key type", parts[1])}
	}
	if parts[2] == "" || !isBase62(parts[2]) {
		return "", &ErrNotAKey{Reason: "the payload is not base62"}
	}
	if want := checksumOf(raw[:strings.LastIndex(raw, "_")]); parts[3] != want {
		return "", &ErrNotAKey{Reason: "the checksum does not match, so this is a mistyped or truncated key"}
	}
	return keyType, nil
}

// LooksLikeKey reports whether a string is shaped like an Orama key at all,
// checksum included. It is what tells a credential apart from a JWT or a
// wallet address without a lookup.
func LooksLikeKey(raw string) bool {
	_, err := ParseKey(raw)
	return err == nil
}

// IsLegacyKey reports whether a string is a key in the old `ak_<payload>:<ns>`
// format. Those keys are still live — they are rows like any other — and this
// is how the paths that read a key's shape (rather than its row) tell.
func IsLegacyKey(raw string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(raw)), "ak_")
}

// checksumOf is CRC32 over the key's body, base62.
//
// CRC32 is not a security property and is not doing any work as one: anybody
// can compute it. What it buys is that a random string is not mistaken for a
// key, and a truncated one is refused as truncated.
func checksumOf(body string) string {
	return base62Encode(new(big.Int).SetUint64(uint64(crc32.ChecksumIEEE([]byte(body)))))
}

func base62Encode(n *big.Int) string {
	if n.Sign() == 0 {
		return "0"
	}
	base := big.NewInt(62)
	mod := new(big.Int)
	value := new(big.Int).Set(n)

	var out []byte
	for value.Sign() > 0 {
		value.DivMod(value, base, mod)
		out = append([]byte{base62Alphabet[mod.Int64()]}, out...)
	}
	return string(out)
}

func isBase62(s string) bool {
	for _, c := range s {
		if !strings.ContainsRune(base62Alphabet, c) {
			return false
		}
	}
	return true
}

// IsWalletSubject reports whether a JWT subject is a wallet address rather than
// an API key.
//
// It is a positive test, and IsAPIKeySubject is its negative. That way round
// matters: a subject nothing recognises is then treated as a key, which holds
// only what its row says, rather than as a logged-in user — and a logged-in
// user is what the data-plane grants are gated on.
//
// The old test was `strings.HasPrefix(sub, "ak_")`. Every key minted from here
// on starts with `orama_`, so that test would have called an exchanged-key JWT
// a wallet, and hasWalletJWT is exactly the check that stops an extracted
// runtime key acting as a logged-in user.
//
// It does not check the length of an EVM address. The property that has to hold
// is that no key is ever read as a wallet, and no key shape begins `0x`;
// demanding forty hex digits would buy nothing, because a subject is never
// chosen by the caller — it comes from a verified signature or from the
// exchange path — and would refuse a wallet whose address this code had
// mis-cased somewhere upstream.
func IsWalletSubject(sub string) bool {
	sub = strings.TrimSpace(sub)
	if strings.HasPrefix(strings.ToLower(sub), "0x") {
		return true
	}
	// A Solana address, in whatever case NormalizeWallet left it. An underscore
	// is not in the base58 alphabet, so no key shape can reach here.
	if len(sub) < 32 || len(sub) > 44 {
		return false
	}
	for _, c := range sub {
		if !strings.ContainsRune(base58SubjectAlphabet, c) {
			return false
		}
	}
	return true
}

// base58SubjectAlphabet is base58 plus the lowercase forms of its uppercase
// letters, because a wallet subject is stored normalised.
const base58SubjectAlphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

// IsAPIKeySubject reports whether a JWT subject is an API key rather than a
// logged-in user.
func IsAPIKeySubject(sub string) bool {
	sub = strings.TrimSpace(sub)
	return sub != "" && !IsWalletSubject(sub)
}

// KeyFingerprint names a credential without being one.
//
// HashAPIKey returns the key unchanged when no HMAC secret is configured — it
// has to, because that is the value in the api_keys column on a cluster that
// has not been given one — so it is the wrong thing to put in a response. This
// is always a hash, so a gateway missing a secret cannot echo a credential.
func KeyFingerprint(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(key))
	return "key_" + hex.EncodeToString(sum[:6])
}
