package auth

import (
	"context"
	"crypto/ed25519"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"go.uber.org/zap"
)

// Which key signed a token, and what it was allowed to sign.
//
// The Ed25519 signing key used to be HKDF-derived from the cluster secret with
// a fixed label. Every node and every namespace gateway holds that secret, so
// every one of them could mint a token for any namespace and any subject — and
// there was nothing to rotate to, because the derivation had one output.
//
// Each gateway generates its own key now. A namespace gateway's key is bound to
// its namespace: a token signed with it is refused unless its `namespace` claim
// matches. The index gateway's key is bound to nothing, because the index
// gateway is the control plane and a compromise of it is not a tenant boundary
// problem.

// KeyIDPrefix is what a kid starts with. It says which algorithm the key is
// without having to look the key up first.
const KeyIDPrefix = "ed_"

// SigningKey is one key this cluster will accept a token from.
type SigningKey struct {
	// KID is the `kid` in a token header.
	KID string
	// Namespace this key may sign for. Empty means the index gateway's key,
	// which is not bound to one.
	Namespace string
	// Public is the verifying half.
	Public ed25519.PublicKey
	// RetiredAt is when the key stops being accepted, or zero while it is
	// live. A rotation sets it on the outgoing key rather than deleting it, so
	// tokens already issued keep working for their remaining lifetime.
	RetiredAt time.Time
}

// Live reports whether a key is still accepted at the given moment.
func (k SigningKey) Live(now time.Time) bool {
	return k.RetiredAt.IsZero() || now.Before(k.RetiredAt)
}

// Binds reports whether a token claiming this namespace may be signed by this
// key.
//
// An unbound key — the index gateway's — signs for any namespace. A bound key
// signs only for its own, which is what stops one tenant's gateway minting a
// token for another.
func (k SigningKey) Binds(namespace string) bool {
	return k.Namespace == "" || strings.EqualFold(k.Namespace, namespace)
}

// KeyIDFor is the kid of a public key. It is derived from the key so two
// gateways cannot collide and nobody can choose one.
func KeyIDFor(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return KeyIDPrefix + hex.EncodeToString(sum[:8])
}

// encodePublicKey is how a public key appears in the registry and in the JWKS.
func encodePublicKey(pub ed25519.PublicKey) string {
	return base64.RawURLEncoding.EncodeToString(pub)
}

func decodePublicKey(encoded string) (ed25519.PublicKey, error) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return nil, fmt.Errorf("the stored public key is not base64url: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("the stored public key is %d bytes, want %d", len(raw), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(raw), nil
}

// signingKeyReloadInterval is how often the published keys are re-read.
//
// A gateway that has not seen a newly published key yet refuses tokens signed
// with it, so this is the delay between a namespace gateway starting and its
// tokens being accepted elsewhere. It is short for the same reason the
// revocation list's is: the cost is one query, and the alternative is a
// rollout that looks like an outage.
const signingKeyReloadInterval = 30 * time.Second

// SigningKeys is every key this gateway will verify a token from.
type SigningKeys struct {
	orm    client.NetworkClient
	logger *logging.ColoredLogger

	mu        sync.RWMutex
	keys      map[string]SigningKey
	loadedAt  time.Time
	now       func() time.Time
	published map[string]struct{}
}

// NewSigningKeys returns an empty set. A nil orm makes it local-only, which is
// the single-node and test case: the gateway still verifies its own key.
func NewSigningKeys(orm client.NetworkClient, logger *logging.ColoredLogger) *SigningKeys {
	return &SigningKeys{
		orm:       orm,
		logger:    logger,
		keys:      map[string]SigningKey{},
		now:       time.Now,
		published: map[string]struct{}{},
	}
}

// Add puts a key in the set without publishing it. It is how a gateway trusts
// its own key, and how the legacy derived key is accepted for the length of one
// token lifetime after an upgrade.
func (s *SigningKeys) Add(key SigningKey) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys[key.KID] = key
}

// Publish records a key so every other gateway will accept tokens signed with
// it. Publishing the same key twice is a no-op, which is what makes a restart
// cheap.
func (s *SigningKeys) Publish(ctx context.Context, key SigningKey) error {
	s.Add(key)
	if s == nil || s.orm == nil {
		return nil
	}
	db := s.orm.Database()
	if db == nil {
		return nil
	}

	var namespace any
	if key.Namespace != "" {
		namespace = key.Namespace
	}
	if _, err := db.Query(client.WithInternalAuth(ctx),
		`INSERT INTO signing_keys(kid, namespace, algorithm, public_key)
		 VALUES (?, ?, 'EdDSA', ?)
		 ON CONFLICT(kid) DO UPDATE SET retired_at = NULL`,
		key.KID, namespace, encodePublicKey(key.Public)); err != nil {
		return fmt.Errorf("publish the signing key %s: %w", key.KID, err)
	}

	s.mu.Lock()
	s.published[key.KID] = struct{}{}
	s.mu.Unlock()
	return nil
}

// Retire sets when a key stops being accepted. A rotation calls it on the
// outgoing key rather than deleting it, so tokens already issued keep working
// until they expire on their own.
func (s *SigningKeys) Retire(ctx context.Context, kid string, at time.Time) error {
	if s == nil || s.orm == nil {
		return nil
	}
	db := s.orm.Database()
	if db == nil {
		return nil
	}
	if _, err := db.Query(client.WithInternalAuth(ctx),
		`UPDATE signing_keys SET retired_at = ? WHERE kid = ?`,
		at.UTC().Format("2006-01-02 15:04:05"), kid); err != nil {
		return fmt.Errorf("retire the signing key %s: %w", kid, err)
	}
	return nil
}

// Lookup returns the key a kid names, if this gateway will accept it.
func (s *SigningKeys) Lookup(kid string) (SigningKey, bool) {
	if s == nil {
		return SigningKey{}, false
	}
	s.refreshIfStale()

	s.mu.RLock()
	key, ok := s.keys[kid]
	s.mu.RUnlock()
	if !ok || !key.Live(s.now()) {
		return SigningKey{}, false
	}
	return key, true
}

// All returns every live key, for the JWKS.
func (s *SigningKeys) All() []SigningKey {
	if s == nil {
		return nil
	}
	s.refreshIfStale()

	s.mu.RLock()
	defer s.mu.RUnlock()
	now := s.now()
	out := make([]SigningKey, 0, len(s.keys))
	for _, key := range s.keys {
		if key.Live(now) {
			out = append(out, key)
		}
	}
	return out
}

// refreshIfStale re-reads the published keys when the cached set has aged out.
//
// A failed read keeps the previous set rather than emptying it: forgetting
// every key because one query failed would refuse every token in the cluster.
func (s *SigningKeys) refreshIfStale() {
	s.mu.RLock()
	fresh := s.now().Sub(s.loadedAt) < signingKeyReloadInterval
	s.mu.RUnlock()
	if fresh {
		return
	}
	if err := s.Reload(context.Background()); err != nil && s.logger != nil {
		s.logger.ComponentWarn(logging.ComponentGeneral,
			"could not read the published signing keys; keeping the ones already known", zap.Error(err))
	}
}

// Reload reads every published key.
func (s *SigningKeys) Reload(ctx context.Context) error {
	if s == nil {
		return nil
	}
	// The clock moves whether or not there is a database, or a gateway with
	// none re-reads on every single verification.
	defer func() {
		s.mu.Lock()
		s.loadedAt = s.now()
		s.mu.Unlock()
	}()

	if s.orm == nil {
		return nil
	}
	db := s.orm.Database()
	if db == nil {
		return nil
	}

	res, err := db.Query(client.WithInternalAuth(ctx),
		`SELECT kid, namespace, public_key, retired_at FROM signing_keys`)
	if err != nil {
		return fmt.Errorf("read the published signing keys: %w", err)
	}

	loaded := map[string]SigningKey{}
	if res != nil {
		for _, row := range res.Rows {
			if len(row) < 4 {
				continue
			}
			pub, err := decodePublicKey(cellString(row[2]))
			if err != nil {
				if s.logger != nil {
					s.logger.ComponentWarn(logging.ComponentGeneral,
						"a published signing key could not be read", zap.String("kid", cellString(row[0])), zap.Error(err))
				}
				continue
			}
			loaded[cellString(row[0])] = SigningKey{
				KID:       cellString(row[0]),
				Namespace: cellString(row[1]),
				Public:    pub,
				RetiredAt: retirementFrom(row[3]),
			}
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	// A key this gateway added locally — its own, and the legacy derived one —
	// is kept even when it is not published, so a gateway can always verify
	// what it just minted.
	for kid, key := range s.keys {
		if _, published := s.published[kid]; !published {
			loaded[kid] = key
		}
	}
	s.keys = loaded
	return nil
}

// retirementFrom reads a retired_at column.
//
// NULL is "not retired" and is the common case. A value that is present and
// cannot be read is treated as retired at the epoch — refused — rather than as
// live: a key somebody retired and a key nobody can parse are the same risk,
// and the failure is visible immediately instead of a year later.
func retirementFrom(cell any) time.Time {
	switch value := cell.(type) {
	case nil:
		return time.Time{}
	case time.Time:
		return value.UTC()
	}

	text := strings.TrimSpace(cellString(cell))
	if text == "" {
		return time.Time{}
	}
	for _, layout := range []string{"2006-01-02 15:04:05", time.RFC3339, "2006-01-02 15:04:05Z07:00", "2006-01-02 15:04:05 -0700 MST"} {
		if t, err := time.Parse(layout, text); err == nil {
			return t.UTC()
		}
	}
	return time.Unix(0, 0).UTC()
}

func cellString(v any) string {
	switch value := v.(type) {
	case string:
		return value
	case []byte:
		return string(value)
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", value)
	}
}

// Rotate generates a new signing key, publishes it, starts signing with it, and
// leaves the outgoing one verifiable until the tokens it signed have expired.
//
// Two kids are in flight for exactly that window. Retiring the old key
// immediately would refuse every token already issued; never retiring it would
// leave a key that can sign this namespace's tokens on disk for ever.
func (s *Service) Rotate(ctx context.Context, dataDir string) (SigningKey, error) {
	if s.edSigningKey == nil {
		return SigningKey{}, fmt.Errorf("this gateway does not sign tokens, so it has no key to rotate")
	}
	previous := s.edKeyID

	pub, priv, err := ed25519.GenerateKey(cryptorand.Reader)
	if err != nil {
		return SigningKey{}, fmt.Errorf("generate the replacement signing key: %w", err)
	}

	next := SigningKey{KID: KeyIDFor(pub), Namespace: s.edKeyNamespace, Public: pub}
	// Published before it signs anything: a token signed with a key the rest
	// of the cluster has not seen is refused everywhere until the next reload.
	if err := s.signingKeys.Publish(ctx, next); err != nil {
		return SigningKey{}, err
	}
	if err := persistSigningKey(dataDir, priv); err != nil {
		return SigningKey{}, err
	}

	s.SetEdDSAKey(priv, s.edKeyNamespace)

	// Retired after the switch, so there is no moment where neither key is
	// accepted.
	if err := s.signingKeys.Retire(ctx, previous, time.Now().Add(AccessTokenLifetime)); err != nil {
		return next, fmt.Errorf("the new key is live but the previous one was not retired: %w", err)
	}
	return next, nil
}

// eddsaKeyFileName is where a gateway's own signing key lives, relative to its
// data directory. It is named here as well as at the loader because a rotation
// has to overwrite exactly the file the next boot will read.
const EdDSAKeyFileName = "jwt-eddsa-key.pem"

// persistSigningKey writes a key where only this gateway can read it.
func persistSigningKey(dataDir string, priv ed25519.PrivateKey) error {
	pkcs8Bytes, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return fmt.Errorf("marshal the signing key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8Bytes})

	path := filepath.Join(dataDir, "secrets", EdDSAKeyFileName)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create the secrets directory: %w", err)
	}
	if err := os.WriteFile(path, keyPEM, 0600); err != nil {
		return fmt.Errorf("write the signing key: %w", err)
	}
	return os.Chmod(path, 0600)
}
