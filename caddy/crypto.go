package orama

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// These mirror core/pkg/tlsstore and core/pkg/auth; see the package doc.
const (
	macKeyInfo    = "orama-tls-store-mac-v1"
	sealKeyInfo   = "orama-tls-store-seal-v1"
	sealedPrefix  = "v1."
	sealAADPrefix = "orama-tls-store-v1\n"
	gcmNonceLen   = 12
	keyLen        = 32

	// storeAudience is the audience the store's calls are stamped for.
	storeAudience = "caddy-tls-store"

	acmeMACHeader   = "X-Orama-Coordination-MAC"
	acmeMACV2Header = "X-Orama-ACME-MAC-V2"
	acmeNonceHeader = "X-Orama-ACME-Nonce"
	macV2Header     = "X-Orama-Coordination-MAC-V2"
	macV3Header     = "X-Orama-Coordination-MAC-V3"
	nonceHeader     = "X-Orama-Coordination-Nonce"
	nonceBytes      = 16
	payloadVersion  = "orama-coordination-v2"
)

// readHexKey reads a hex key file install wrote.
func readHexKey(path, what string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%s: read key_file: %w", what, err)
	}
	key, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil || len(key) == 0 {
		return nil, fmt.Errorf("%s: key_file %s does not hold a hex key", what, path)
	}
	return key, nil
}

// storeKeys splits the store's master key into its MAC and sealing keys.
func storeKeys(master []byte) (mac, seal []byte, err error) {
	if len(master) != keyLen {
		return nil, nil, fmt.Errorf("TLS store master key is %d bytes, want %d", len(master), keyLen)
	}
	if mac, err = hkdf.Expand(sha256.New, master, macKeyInfo, keyLen); err != nil {
		return nil, nil, fmt.Errorf("derive the TLS store MAC key: %w", err)
	}
	if seal, err = hkdf.Expand(sha256.New, master, sealKeyInfo, keyLen); err != nil {
		return nil, nil, fmt.Errorf("derive the TLS store seal key: %w", err)
	}
	return mac, seal, nil
}

// sealValue encrypts value for storage under key, bound to the key.
func sealValue(sealKey []byte, key string, value []byte) (string, error) {
	aead, err := newAEAD(sealKey)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcmNonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("draw a nonce to seal %s: %w", key, err)
	}
	return sealedPrefix + base64.StdEncoding.EncodeToString(aead.Seal(nonce, nonce, value, []byte(sealAADPrefix+key))), nil
}

// openValue decrypts a value sealValue made for key.
func openValue(sealKey []byte, key, sealed string) ([]byte, error) {
	body, ok := strings.CutPrefix(sealed, sealedPrefix)
	if !ok {
		return nil, fmt.Errorf("stored value of %s is not sealed", key)
	}
	raw, err := base64.StdEncoding.DecodeString(body)
	if err != nil || len(raw) < gcmNonceLen+16 {
		return nil, fmt.Errorf("stored value of %s is malformed", key)
	}
	aead, err := newAEAD(sealKey)
	if err != nil {
		return nil, err
	}
	plain, err := aead.Open(nil, raw[:gcmNonceLen], raw[gcmNonceLen:], []byte(sealAADPrefix+key))
	if err != nil {
		return nil, fmt.Errorf("stored value of %s does not open with this cluster's key "+
			"(sealed under another cluster secret, or stored under another key)", key)
	}
	return plain, nil
}

func newAEAD(sealKey []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(sealKey)
	if err != nil {
		return nil, fmt.Errorf("TLS store seal key: %w", err)
	}
	return cipher.NewGCM(block)
}

// hmacHex is the hex HMAC-SHA256 of payload under key.
func hmacHex(key []byte, payload string) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

// coordinationV2MAC is the MAC core/pkg/auth's coordination v2 stamp carries.
func coordinationV2MAC(key []byte, method, audience, path, query string, body []byte, nonce string, ts int64) string {
	sum := sha256.Sum256(body)
	return hmacHex(key, strings.Join([]string{payloadVersion, strings.ToUpper(method), audience, path, query,
		hex.EncodeToString(sum[:]), nonce, strconv.FormatInt(ts, 10)}, "\n"))
}

// coordinationV3MAC is the MAC core/pkg/auth's coordination v3 stamp carries:
// the v2 payload plus scope, the port of the gateway process the call is for.
func coordinationV3MAC(key []byte, method, audience, scope, path, query string, body []byte, nonce string, ts int64) string {
	sum := sha256.Sum256(body)
	return hmacHex(key, strings.Join([]string{"orama-coordination-v3", strings.ToUpper(method), audience, scope, path, query,
		hex.EncodeToString(sum[:]), nonce, strconv.FormatInt(ts, 10)}, "\n"))
}

// requestPort is the port req is sent to: the URL's, or the scheme's default.
func requestPort(req *http.Request) string {
	if port := req.URL.Port(); port != "" {
		return port
	}
	switch req.URL.Scheme {
	case "http":
		return "80"
	case "https":
		return "443"
	}
	return ""
}

// signV2 stamps req as core/pkg/auth.VerifyCoordinationV2 checks it, for
// audience, with a fresh nonce: the v3 stamp, which is good for the gateway
// process on req's port only, and beside it the v2 stamp a gateway built
// before v3 reads.
func signV2(key []byte, req *http.Request, body []byte, audience string, now time.Time) error {
	raw := make([]byte, nonceBytes)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Errorf("draw a request nonce: %w", err)
	}
	nonce, ts := hex.EncodeToString(raw), now.Unix()
	req.Header.Set(nonceHeader, nonce)
	req.Header.Set(macV2Header, strconv.FormatInt(ts, 10)+"."+
		coordinationV2MAC(key, req.Method, audience, req.URL.Path, req.URL.RawQuery, body, nonce, ts))
	req.Header.Set(macV3Header, strconv.FormatInt(ts, 10)+"."+
		coordinationV3MAC(key, req.Method, audience, requestPort(req), req.URL.Path, req.URL.RawQuery, body, nonce, ts))
	return nil
}
