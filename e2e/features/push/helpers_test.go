//go:build e2e_fleet

package push

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"net/http"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

// Routes (docs/whitepaper/technical-reference/appendices/i-api-surface.md#api-surface) and limits (core/pkg/gateway/handlers/push).
const (
	pathCreds      = "/v1/namespace/push-credentials"
	pathCredsAPNs  = "/v1/namespace/push-credentials/apns"
	pathCredsNtfy  = "/v1/namespace/push-credentials/ntfy"
	pathConfig     = "/v1/push/config"
	pathDevices    = "/v1/push/devices"
	pathSend       = "/v1/push/send"
	pathTopics     = "/v1/push/topics"
	pathTopicsSend = "/v1/push/topics/send"
	maxToken       = 512
	maxRegister    = 4096
	maxCredsBody   = 32 << 10
	maxConfigBody  = 16 << 10
	maxSendBody    = 64 << 10
	topicTTL       = 7 * 24 * time.Hour
	day            = 24 * time.Hour
	pollEvery      = 2 * time.Second
	deliverBudget  = time.Minute
)

// p8Key is a fresh PKCS#8 EC P-256 key in PEM, as Apple issues .p8 files.
func p8Key(t testing.TB) string {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

func apnsCreds(t testing.TB, key string) map[string]any {
	t.Helper()
	return map[string]any{"team_id": "E2ETEAM001", "key_id": "E2EKEY0001", "bundle_id": "bid.e2e.app", "p8_key": key, "environment": "sandbox"}
}

// hexSecret is n random bytes, hex-encoded, and the topic id they name
// (lowercase hex SHA-256 of the decoded bytes).
func hexSecret(t testing.TB, n int) (secret, topicID string) {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(b), hex.EncodeToString(sum[:])
}

func randomTopic(t testing.TB) string {
	t.Helper()
	s, _ := hexSecret(t, 16)
	return "e2e" + s
}

// del sends a DELETE with a JSON body.
func del(t testing.TB, c *gw.Client, path string, who tenancy.Cred, body any) *gw.Response {
	t.Helper()
	return tenancy.Send(t, c, http.MethodDelete, path, who, body)
}

// put sends a PUT with a JSON body.
func put(t testing.TB, c *gw.Client, path string, who tenancy.Cred, body any) *gw.Response {
	t.Helper()
	return tenancy.Send(t, c, http.MethodPut, path, who, body)
}

// status fails unless r has one of want.
func status(t testing.TB, what string, r *gw.Response, want ...int) {
	t.Helper()
	for _, w := range want {
		if r.Status == w {
			return
		}
	}
	t.Errorf("%s: want %v, got %d %.300s", what, want, r.Status, r.Body)
}
