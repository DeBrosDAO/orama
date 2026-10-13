//go:build e2e_fleet

package services

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

// The gateway's vault routes (docs/whitepaper/technical-reference/appendices/i-api-surface.md#vault) and the ownership
// proof they check (core/pkg/gateway/handlers/vault/ownership.go; the same
// messages sdk-vault's crypto/ownership.ts signs).
const (
	VaultPush   = "/v1/vault/push"
	VaultPull   = "/v1/vault/pull"
	VaultStatus = "/v1/vault/status"
	VaultHealth = "/v1/vault/health"
	pushMsgFmt  = "vault-push-v1:%s:%d"
	pullMsgFmt  = "vault-pull-v1:%s:%d"
)

// VaultOwner is an Ed25519 identity: identity = hex(SHA-256(public key)).
type VaultOwner struct {
	Pub  ed25519.PublicKey
	priv ed25519.PrivateKey
}

// NewVaultOwner is a fresh identity.
func NewVaultOwner(t testing.TB) *VaultOwner {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return &VaultOwner{Pub: pub, priv: priv}
}

// Identity is the owner's 64-hex identity.
func (o *VaultOwner) Identity() string {
	sum := sha256.Sum256(o.Pub)
	return hex.EncodeToString(sum[:])
}

// PushBody is a push signed by signer for identity (normally o itself).
func (o *VaultOwner) PushBody(identity string, version uint64, envelope []byte, signer *VaultOwner) map[string]any {
	msg := fmt.Sprintf(pushMsgFmt, identity, version)
	return map[string]any{
		"identity":  identity,
		"envelope":  base64.StdEncoding.EncodeToString(envelope),
		"version":   version,
		"pubkey":    hex.EncodeToString(signer.Pub),
		"signature": hex.EncodeToString(ed25519.Sign(signer.priv, []byte(msg))),
	}
}

// PullBody is a pull at time at, signed by signer for identity.
func (o *VaultOwner) PullBody(identity string, at time.Time, signer *VaultOwner) map[string]any {
	ts := at.Unix()
	msg := fmt.Sprintf(pullMsgFmt, identity, ts)
	return map[string]any{
		"identity":  identity,
		"pubkey":    hex.EncodeToString(signer.Pub),
		"signature": hex.EncodeToString(ed25519.Sign(signer.priv, []byte(msg))),
		"timestamp": ts,
	}
}

// PushResult is the gateway's push answer.
type PushResult struct {
	Status    string `json:"status"`
	AckCount  int    `json:"ack_count"`
	Total     int    `json:"total"`
	Quorum    int    `json:"quorum"`
	Threshold int    `json:"threshold"`
}

// PullResult is the gateway's pull answer.
type PullResult struct {
	Envelope  string `json:"envelope"`
	Collected int    `json:"collected"`
	Threshold int    `json:"threshold"`
}

// PostJSON posts body (marshalled unless []byte) anonymously.
func PostJSON(t testing.TB, c *gw.Client, path string, body any) *gw.Response {
	t.Helper()
	raw, ok := body.([]byte)
	if !ok {
		var err error
		if raw, err = json.Marshal(body); err != nil {
			t.Fatal(err)
		}
	}
	return c.MustSend(t, gw.Req{Method: http.MethodPost, Path: path, Body: raw,
		Header: http.Header{"Content-Type": {"application/json"}}})
}
