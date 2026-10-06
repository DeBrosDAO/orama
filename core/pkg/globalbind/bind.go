package globalbind

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"regexp"

	"filippo.io/edwards25519"
	"github.com/DeBrosOfficial/network/pkg/clusterreg"
	secp256k1 "github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
)

// Prefix is the domain separator on every service-key binding. It matches
// chain/x/nodes/types.BindingPrefix. core does not import that module.
const Prefix = "orama-global-bind-v1"

const (
	KeyTypeSecp256k1       = "secp256k1"
	KeyTypeEd25519         = "ed25519"
	KeyTypeEd25519Expanded = "ed25519-expanded"
)

var servicePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

// Binding is a signature and the public key that made it. The private key is not here.
type Binding struct {
	Service   string
	KeyType   string
	Pubkey    []byte
	Signature []byte
}

// SignBytes is the ASCII statement the service key signs:
// orama-global-bind-v1|chain-id|operator|service|hex(pubkey).
// pubkey hex is lowercase.
func SignBytes(chainID, operator, service string, pubkey []byte) []byte {
	return []byte(fmt.Sprintf("%s|%s|%s|%s|%s", Prefix, chainID, operator, service, hex.EncodeToString(pubkey)))
}

// SignSecp256k1 signs with a 32-byte secret. The signature is R||S, S low,
// over SHA-256 of the statement: the digest x/nodes verifies.
func SignSecp256k1(secret []byte, chainID, operator, service string) (Binding, error) {
	if len(secret) != 32 {
		return Binding{}, fmt.Errorf("secp256k1 secret is %d bytes, want 32", len(secret))
	}
	priv := secp256k1.PrivKeyFromBytes(secret)
	pub := priv.PubKey().SerializeCompressed()
	sig, err := signStatement(KeyTypeSecp256k1, pub, func(msg []byte) ([]byte, error) {
		sum := sha256.Sum256(msg)
		compact := ecdsa.SignCompact(priv, sum[:], false)
		return compact[1:], nil
	}, chainID, operator, service)
	return sig, err
}

// SignEd25519 signs with a 32-byte seed.
func SignEd25519(seed []byte, chainID, operator, service string) (Binding, error) {
	if len(seed) != ed25519.SeedSize {
		return Binding{}, fmt.Errorf("ed25519 seed is %d bytes, want %d", len(seed), ed25519.SeedSize)
	}
	priv := ed25519.NewKeyFromSeed(seed)
	pub := priv.Public().(ed25519.PublicKey)
	return signStatement(KeyTypeEd25519, pub, func(msg []byte) ([]byte, error) {
		return ed25519.Sign(priv, msg), nil
	}, chainID, operator, service)
}

// SignExpandedEd25519 signs with Tor's 64-byte expanded secret: the clamped
// scalar and the nonce prefix. The signature is a normal 64-byte ed25519
// signature, which is what x/nodes verifies.
func SignExpandedEd25519(expanded []byte, chainID, operator, service string) (Binding, error) {
	if len(expanded) != 64 {
		return Binding{}, fmt.Errorf("expanded ed25519 secret is %d bytes, want 64", len(expanded))
	}
	pub, err := expandedPublic(expanded)
	if err != nil {
		return Binding{}, err
	}
	return signStatement(KeyTypeEd25519, pub, func(msg []byte) ([]byte, error) {
		return signExpanded(expanded, pub, msg)
	}, chainID, operator, service)
}

func signStatement(keyType string, pub []byte, sign func([]byte) ([]byte, error), chainID, operator, service string) (Binding, error) {
	if err := validateNames(chainID, operator, service); err != nil {
		return Binding{}, err
	}
	sig, err := sign(SignBytes(chainID, operator, service, pub))
	if err != nil {
		return Binding{}, err
	}
	if len(sig) != 64 {
		return Binding{}, fmt.Errorf("signature is %d bytes, want 64", len(sig))
	}
	return Binding{Service: service, KeyType: keyType, Pubkey: append([]byte(nil), pub...), Signature: sig}, nil
}

func validateNames(chainID, operator, service string) error {
	if chainID == "" {
		return fmt.Errorf("chain id is empty")
	}
	// The chain puts the canonical account string in the statement. Signing any
	// other spelling would not verify.
	if _, err := clusterreg.CanonicalAccount(operator); err != nil {
		return fmt.Errorf("operator: %w", err)
	}
	if !servicePattern.MatchString(service) {
		return fmt.Errorf("service %q must match %s", service, servicePattern.String())
	}
	return nil
}

func expandedPublic(expanded []byte) ([]byte, error) {
	a, err := new(edwards25519.Scalar).SetBytesWithClamping(expanded[:32])
	if err != nil {
		return nil, fmt.Errorf("expanded ed25519 scalar: %w", err)
	}
	return new(edwards25519.Point).ScalarBaseMult(a).Bytes(), nil
}

func signExpanded(expanded, pub, message []byte) ([]byte, error) {
	a, err := new(edwards25519.Scalar).SetBytesWithClamping(expanded[:32])
	if err != nil {
		return nil, err
	}
	h := sha512.New()
	h.Write(expanded[32:])
	h.Write(message)
	r, err := new(edwards25519.Scalar).SetUniformBytes(h.Sum(nil))
	if err != nil {
		return nil, err
	}
	R := new(edwards25519.Point).ScalarBaseMult(r).Bytes()
	h.Reset()
	h.Write(R)
	h.Write(pub)
	h.Write(message)
	k, err := new(edwards25519.Scalar).SetUniformBytes(h.Sum(nil))
	if err != nil {
		return nil, err
	}
	S := new(edwards25519.Scalar).MultiplyAdd(k, a, r)
	sig := make([]byte, 64)
	copy(sig, R)
	copy(sig[32:], S.Bytes())
	return sig, nil
}
