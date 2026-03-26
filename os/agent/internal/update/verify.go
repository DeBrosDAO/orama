package update

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"golang.org/x/crypto/sha3"
)

// SignerAddress is the Ethereum address authorized to sign OramaOS updates.
// Updates signed by any other address are rejected.
const SignerAddress = "0xb5d8a496c8b2412990d7D467E17727fdF5954afC"

// verifySignature verifies an EVM personal_sign signature against the expected signer.
// hashHex is the hex-encoded SHA-256 hash of the update checksum file.
// signatureHex is the 65-byte hex-encoded EVM signature (r || s || v).
func verifySignature(hashHex, signatureHex string) error {
	if SignerAddress == "0x0000000000000000000000000000000000000000" {
		return fmt.Errorf("signer address not configured — refusing unsigned update")
	}

	sigBytes, err := hex.DecodeString(strings.TrimPrefix(signatureHex, "0x"))
	if err != nil {
		return fmt.Errorf("invalid signature hex: %w", err)
	}
	if len(sigBytes) != 65 {
		return fmt.Errorf("invalid signature length: got %d, expected 65", len(sigBytes))
	}

	// Compute EVM personal_sign message hash
	msgHash := personalSignHash(hashHex)

	// Split signature into r, s, v
	r := new(big.Int).SetBytes(sigBytes[:32])
	s := new(big.Int).SetBytes(sigBytes[32:64])
	v := sigBytes[64]
	if v >= 27 {
		v -= 27
	}
	if v > 1 {
		return fmt.Errorf("invalid signature recovery id: %d", v)
	}

	// Recover public key
	pubKey, err := recoverPubkey(msgHash, r, s, v)
	if err != nil {
		return fmt.Errorf("public key recovery failed: %w", err)
	}

	// Derive Ethereum address
	recovered := pubkeyToAddress(pubKey)
	expected := strings.ToLower(strings.TrimPrefix(SignerAddress, "0x"))
	got := strings.ToLower(strings.TrimPrefix(recovered, "0x"))

	if got != expected {
		return fmt.Errorf("update signed by 0x%s, expected 0x%s", got, expected)
	}

	return nil
}

// personalSignHash computes keccak256("\x19Ethereum Signed Message:\n" + len(msg) + msg).
func personalSignHash(message string) []byte {
	prefix := fmt.Sprintf("\x19Ethereum Signed Message:\n%d", len(message))
	h := sha3.NewLegacyKeccak256()
	h.Write([]byte(prefix))
	h.Write([]byte(message))
	return h.Sum(nil)
}

// recoverPubkey recovers the ECDSA public key from a secp256k1 signature.
// Uses the standard EC point recovery algorithm.
func recoverPubkey(hash []byte, r, s *big.Int, v byte) (*ecdsa.PublicKey, error) {
	// secp256k1 curve parameters
	curve := secp256k1Curve()
	N := curve.Params().N
	P := curve.Params().P

	if r.Sign() <= 0 || s.Sign() <= 0 {
		return nil, errors.New("invalid signature: r or s is zero")
	}
	if r.Cmp(N) >= 0 || s.Cmp(N) >= 0 {
		return nil, errors.New("invalid signature: r or s >= N")
	}

	// Step 1: Compute candidate x = r + v*N (v is 0 or 1)
	x := new(big.Int).Set(r)
	if v == 1 {
		x.Add(x, N)
	}
	if x.Cmp(P) >= 0 {
		return nil, errors.New("invalid recovery: x >= P")
	}

	// Step 2: Recover the y coordinate from x
	Rx, Ry, err := decompressPoint(curve, x)
	if err != nil {
		return nil, fmt.Errorf("point decompression failed: %w", err)
	}

	// The y parity must match the recovery id
	if Ry.Bit(0) != 0 {
		Ry.Sub(P, Ry) // negate y
	}
	// v determines which y we want: v=0 → even y, v=1 → odd y for the first candidate
	// Actually for EVM: v just selects x=r vs x=r+N. y is chosen to make verification work.
	// We try both y values and verify.
	for _, negateY := range []bool{false, true} {
		testRy := new(big.Int).Set(Ry)
		if negateY {
			testRy.Sub(P, testRy)
		}

		// Step 3: Compute public key: Q = r^(-1) * (s*R - e*G)
		rInv := new(big.Int).ModInverse(r, N)
		if rInv == nil {
			return nil, errors.New("r has no modular inverse")
		}

		// s * R
		sRx, sRy := curve.ScalarMult(Rx, testRy, s.Bytes())

		// e * G (where e = hash interpreted as big.Int)
		e := new(big.Int).SetBytes(hash)
		eGx, eGy := curve.ScalarBaseMult(e.Bytes())

		// s*R - e*G = s*R + (-e*G)
		negEGy := new(big.Int).Sub(P, eGy)
		qx, qy := curve.Add(sRx, sRy, eGx, negEGy)

		// Q = r^(-1) * (s*R - e*G)
		qx, qy = curve.ScalarMult(qx, qy, rInv.Bytes())

		// Verify: the recovered key should produce a valid signature
		pub := &ecdsa.PublicKey{Curve: curve, X: qx, Y: qy}
		if ecdsa.Verify(pub, hash, r, s) {
			return pub, nil
		}
	}

	return nil, errors.New("could not recover public key from signature")
}

// pubkeyToAddress derives an Ethereum address from a public key.
// address = keccak256(uncompressed_pubkey_bytes[1:])[12:]
func pubkeyToAddress(pub *ecdsa.PublicKey) string {
	pubBytes := elliptic.Marshal(pub.Curve, pub.X, pub.Y)
	h := sha3.NewLegacyKeccak256()
	h.Write(pubBytes[1:]) // skip 0x04 prefix
	hash := h.Sum(nil)
	return "0x" + hex.EncodeToString(hash[12:])
}

// decompressPoint recovers the y coordinate from x on the given curve.
// Solves y² = x³ + 7 (secp256k1: a=0, b=7).
func decompressPoint(curve elliptic.Curve, x *big.Int) (*big.Int, *big.Int, error) {
	P := curve.Params().P

	// y² = x³ + b mod P
	x3 := new(big.Int).Mul(x, x)
	x3.Mul(x3, x)
	x3.Mod(x3, P)

	// b = 7 for secp256k1
	b := big.NewInt(7)
	y2 := new(big.Int).Add(x3, b)
	y2.Mod(y2, P)

	// y = sqrt(y²) mod P
	// For P ≡ 3 (mod 4), sqrt(a) = a^((P+1)/4) mod P
	// secp256k1's P ≡ 3 (mod 4), so this works.
	exp := new(big.Int).Add(P, big.NewInt(1))
	exp.Rsh(exp, 2) // (P+1)/4
	y := new(big.Int).Exp(y2, exp, P)

	// Verify
	verify := new(big.Int).Mul(y, y)
	verify.Mod(verify, P)
	if verify.Cmp(y2) != 0 {
		return nil, nil, fmt.Errorf("x=%s is not on the curve", x.Text(16))
	}

	return x, y, nil
}

// secp256k1Curve returns the secp256k1 elliptic curve used by Ethereum.
// Go's standard library doesn't include secp256k1, so we define it here.
func secp256k1Curve() elliptic.Curve {
	return &secp256k1CurveParams
}

var secp256k1CurveParams = secp256k1CurveImpl{
	CurveParams: &elliptic.CurveParams{
		P:       hexBigInt("FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFEFFFFFC2F"),
		N:       hexBigInt("FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFEBAAEDCE6AF48A03BBFD25E8CD0364141"),
		B:       big.NewInt(7),
		Gx:      hexBigInt("79BE667EF9DCBBAC55A06295CE870B07029BFCDB2DCE28D959F2815B16F81798"),
		Gy:      hexBigInt("483ADA7726A3C4655DA4FBFC0E1108A8FD17B448A68554199C47D08FFB10D4B8"),
		BitSize: 256,
		Name:    "secp256k1",
	},
}

type secp256k1CurveImpl struct {
	*elliptic.CurveParams
}

func hexBigInt(s string) *big.Int {
	n, _ := new(big.Int).SetString(s, 16)
	return n
}
