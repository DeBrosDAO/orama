package auth

import (
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
)

// A device key is the key pair one installation of an application holds, and
// the thing a device-bound session is bound to.
//
// The device id is never something the client says. It is the RFC 7638
// thumbprint of the device's public key — a hash of the key's own members in a
// fixed order — so a client can name only a device whose key it presents, and
// every signature the gateway checks is checked against the key the id was
// computed from.
//
// Two algorithms, because those are what the platforms hold in hardware:
// ECDSA P-256 (ES256) is what iOS's Secure Enclave, Android's StrongBox and
// WebCrypto give a non-extractable key for, and Ed25519 is accepted for the
// platforms that have it.

const (
	// DeviceIDLength is the length of a thumbprint: base64url, no padding, of
	// a SHA-256.
	DeviceIDLength = 43

	// maxDeviceKeyBytes bounds a JWK before it is parsed. A P-256 JWK is about
	// 130 bytes; this leaves room for members the thumbprint ignores.
	maxDeviceKeyBytes = 1024

	p256CoordinateBytes = 32
	p256RawSignature    = 2 * p256CoordinateBytes
)

var (
	// ErrDeviceKeyInvalid is a device key the gateway cannot use: not a JWK,
	// an algorithm it does not accept, or a point that is not on the curve.
	ErrDeviceKeyInvalid = errors.New("device key is not a P-256 or Ed25519 public JWK")

	// ErrDeviceSignatureInvalid is a signature that does not verify under the
	// device's key.
	ErrDeviceSignatureInvalid = errors.New("the device signature does not verify under the device key")
)

// DeviceKey is a device's public key.
type DeviceKey struct {
	id        string
	canonical string
	ecdsa     *ecdsa.PublicKey
	ed25519   ed25519.PublicKey
}

// jwk is the members a device key may carry. Anything else in the object is
// ignored, as RFC 7638 ignores it for the thumbprint.
type jwk struct {
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
	D   string `json:"d"`
}

// ParseDeviceKey reads a public JWK.
//
// A JWK carrying `d` is refused rather than stripped: a client that sent its
// private key has leaked it, and binding a session to a leaked key is binding
// it to nothing.
func ParseDeviceKey(raw []byte) (*DeviceKey, error) {
	if len(raw) == 0 || len(raw) > maxDeviceKeyBytes {
		return nil, fmt.Errorf("%w: expected a JSON object of at most %d bytes", ErrDeviceKeyInvalid, maxDeviceKeyBytes)
	}
	var k jwk
	if err := json.Unmarshal(raw, &k); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDeviceKeyInvalid, err)
	}
	if k.D != "" {
		return nil, fmt.Errorf("%w: it carries a private key (d); send the public half only", ErrDeviceKeyInvalid)
	}
	switch {
	case k.Kty == "EC" && k.Crv == "P-256":
		return parseP256(k)
	case k.Kty == "OKP" && k.Crv == "Ed25519":
		return parseEd25519(k)
	default:
		return nil, fmt.Errorf("%w: kty %q crv %q", ErrDeviceKeyInvalid, k.Kty, k.Crv)
	}
}

func parseP256(k jwk) (*DeviceKey, error) {
	x, errX := decodeFixed(k.X, p256CoordinateBytes)
	y, errY := decodeFixed(k.Y, p256CoordinateBytes)
	if errX != nil || errY != nil {
		return nil, fmt.Errorf("%w: x and y must each be %d bytes of base64url", ErrDeviceKeyInvalid, p256CoordinateBytes)
	}
	// crypto/ecdh refuses a point that is not on the curve, which is the check
	// that matters: an invalid point is how invalid-curve attacks start.
	point := append(append([]byte{0x04}, x...), y...)
	if _, err := ecdh.P256().NewPublicKey(point); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDeviceKeyInvalid, err)
	}
	canonical := `{"crv":"P-256","kty":"EC","x":"` + k.X + `","y":"` + k.Y + `"}`
	return &DeviceKey{
		id:        thumbprint(canonical),
		canonical: canonical,
		ecdsa: &ecdsa.PublicKey{
			Curve: elliptic.P256(),
			X:     new(big.Int).SetBytes(x),
			Y:     new(big.Int).SetBytes(y),
		},
	}, nil
}

func parseEd25519(k jwk) (*DeviceKey, error) {
	x, err := decodeFixed(k.X, ed25519.PublicKeySize)
	if err != nil {
		return nil, fmt.Errorf("%w: x must be %d bytes of base64url", ErrDeviceKeyInvalid, ed25519.PublicKeySize)
	}
	if hasSmallOrder(x) {
		return nil, fmt.Errorf("%w: a small-order Ed25519 point verifies signatures anybody can make", ErrDeviceKeyInvalid)
	}
	canonical := `{"crv":"Ed25519","kty":"OKP","x":"` + k.X + `"}`
	return &DeviceKey{id: thumbprint(canonical), canonical: canonical, ed25519: ed25519.PublicKey(x)}, nil
}

// decodeFixed decodes unpadded base64url of an exact length. The thumbprint is
// computed over the member as sent, so a value with padding or in the other
// alphabet would give the same key two ids.
func decodeFixed(s string, size int) ([]byte, error) {
	b, err := base64.RawURLEncoding.Strict().DecodeString(s)
	if err != nil {
		return nil, err
	}
	if len(b) != size {
		return nil, fmt.Errorf("got %d bytes", len(b))
	}
	return b, nil
}

// thumbprint is RFC 7638: SHA-256 over the required members, lexicographically
// ordered, with no whitespace.
func thumbprint(canonical string) string {
	sum := sha256.Sum256([]byte(canonical))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// ID is the device id: the key's RFC 7638 thumbprint.
func (k *DeviceKey) ID() string { return k.id }

// JWK is the key as stored: exactly the members its thumbprint covers.
func (k *DeviceKey) JWK() string { return k.canonical }

// Verify checks a base64url signature over message.
//
// An ES256 signature is accepted in both encodings a platform produces: the
// 64-byte r||s JOSE uses (WebCrypto, CryptoKit) and ASN.1 DER (Android
// Keystore, SecKey). Which one is unambiguous from the length.
func (k *DeviceKey) Verify(message []byte, signature string) error {
	sig, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(signature, "="))
	if err != nil {
		return fmt.Errorf("%w: the signature is not base64url", ErrDeviceSignatureInvalid)
	}
	if k.ed25519 != nil {
		if len(sig) != ed25519.SignatureSize || !ed25519.Verify(k.ed25519, message, sig) {
			return ErrDeviceSignatureInvalid
		}
		return nil
	}
	digest := sha256.Sum256(message)
	if len(sig) == p256RawSignature {
		r := new(big.Int).SetBytes(sig[:p256CoordinateBytes])
		s := new(big.Int).SetBytes(sig[p256CoordinateBytes:])
		if ecdsa.Verify(k.ecdsa, digest[:], r, s) {
			return nil
		}
	}
	// A DER signature is usually 70-72 bytes, and can be 64: it is tried
	// whenever the raw reading did not verify.
	if ecdsa.VerifyASN1(k.ecdsa, digest[:], sig) {
		return nil
	}
	return ErrDeviceSignatureInvalid
}

// smallOrderEd25519 are the encodings of the Ed25519 points of order 1, 2, 4
// and 8, with the sign bit cleared: libsodium's blocklist. A signature under
// one of them can be forged without any private key — the identity point
// verifies R = identity, S = 0 for every message — so a device holding one
// holds nothing.
var smallOrderEd25519 = [][32]byte{
	{0x00},
	{0x01},
	{0x26, 0xe8, 0x95, 0x8f, 0xc2, 0xb2, 0x27, 0xb0, 0x45, 0xc3, 0xf4, 0x89, 0xf2, 0xef, 0x98, 0xf0,
		0xd5, 0xdf, 0xac, 0x05, 0xd3, 0xc6, 0x33, 0x39, 0xb1, 0x38, 0x02, 0x88, 0x6d, 0x53, 0xfc, 0x05},
	{0xc7, 0x17, 0x6a, 0x70, 0x3d, 0x4d, 0xd8, 0x4f, 0xba, 0x3c, 0x0b, 0x76, 0x0d, 0x10, 0x67, 0x0f,
		0x2a, 0x20, 0x53, 0xfa, 0x2c, 0x39, 0xcc, 0xc6, 0x4e, 0xc7, 0xfd, 0x77, 0x92, 0xac, 0x03, 0x7a},
	{0xec, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
		0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f},
	{0xed, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
		0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f},
	{0xee, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
		0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f},
}

// hasSmallOrder reports whether an encoded Ed25519 point is on the blocklist,
// ignoring the sign bit as the encoding does for these points.
func hasSmallOrder(point []byte) bool {
	var masked [32]byte
	copy(masked[:], point)
	masked[31] &= 0x7f
	for _, blocked := range smallOrderEd25519 {
		if masked == blocked {
			return true
		}
	}
	return false
}

// ValidDeviceID reports whether s has the shape of a device id. It says
// nothing about whether such a device exists.
func ValidDeviceID(s string) bool {
	if len(s) != DeviceIDLength {
		return false
	}
	_, err := base64.RawURLEncoding.Strict().DecodeString(s)
	return err == nil
}
