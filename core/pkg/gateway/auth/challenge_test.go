package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth/siw"
	"github.com/ethereum/go-ethereum/common"
	ethcrypto "github.com/ethereum/go-ethereum/crypto"
	"github.com/mr-tron/base58"
)

// A login is a real signature over a real message, so these tests make both.
// Asserting that a signature of 65 zero bytes is refused proves almost nothing;
// what has to hold is that a genuine signature over the issued text is accepted
// and that every single-field variation of it is not.

const testDomain = "gateway.orama.network"

func ethWallet(t *testing.T) (address string, sign func(string) string) {
	t.Helper()
	key, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	address = ethcrypto.PubkeyToAddress(key.PublicKey).Hex()
	return address, func(message string) string {
		msg := []byte(message)
		prefix := []byte("\x19Ethereum Signed Message:\n" + itoa(len(msg)))
		sig, err := ethcrypto.Sign(ethcrypto.Keccak256(prefix, msg), key)
		if err != nil {
			t.Fatalf("sign: %v", err)
		}
		return "0x" + hex.EncodeToString(sig)
	}
}

func solWallet(t *testing.T) (address string, sign func(string) string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return base58.Encode(pub), func(message string) string {
		return base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte(message)))
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// issuedMessage renders what CreateChallenge would, without a database.
func issuedMessage(t *testing.T, chain siw.Chain, address, namespace string, issuedAt time.Time) string {
	t.Helper()
	chainID := ethChainID
	if chain == siw.Solana {
		chainID = solChainID
	}
	m := &siw.Message{
		Chain:          chain,
		Domain:         testDomain,
		Address:        address,
		Statement:      "Sign in to the " + namespace + " namespace on Orama.",
		URI:            "https://" + testDomain,
		ChainID:        chainID,
		Nonce:          "0123456789abcdef0123456789abcdef",
		IssuedAt:       issuedAt,
		ExpirationTime: issuedAt.Add(ChallengeTTL),
		Resources:      []string{namespaceResourcePrefix + namespace},
	}
	text, err := m.Render()
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	return text
}

func TestVerifySignedMessage_acceptsARealEthereumSignature(t *testing.T) {
	s := &Service{}
	address, sign := ethWallet(t)
	message := issuedMessage(t, siw.Ethereum, address, "acme", time.Now().UTC().Truncate(time.Second))

	m, err := s.VerifySignedMessage(context.Background(), message, sign(message), testDomain)
	if err != nil {
		t.Fatalf("a genuine signature over the issued message was refused: %v", err)
	}
	if m.Address != address {
		t.Errorf("address = %q, want %q", m.Address, address)
	}
	if ns, err := NamespaceOf(m); err != nil || ns != "acme" {
		t.Errorf("namespace = %q, %v", ns, err)
	}
}

func TestVerifySignedMessage_acceptsARealSolanaSignature(t *testing.T) {
	s := &Service{}
	address, sign := solWallet(t)
	message := issuedMessage(t, siw.Solana, address, "acme", time.Now().UTC().Truncate(time.Second))

	m, err := s.VerifySignedMessage(context.Background(), message, sign(message), testDomain)
	if err != nil {
		t.Fatalf("a genuine Solana signature over the issued message was refused: %v", err)
	}
	if m.Chain != siw.Solana {
		t.Errorf("chain = %q", m.Chain)
	}
}

// The domain check is what the format is for: a signature the user made for
// another site is not a login here, however valid it is.
func TestVerifySignedMessage_refusesAMessageSignedForAnotherDomain(t *testing.T) {
	s := &Service{}
	address, sign := ethWallet(t)
	message := issuedMessage(t, siw.Ethereum, address, "acme", time.Now().UTC().Truncate(time.Second))

	_, err := s.VerifySignedMessage(context.Background(), message, sign(message), "evil.example")
	if !errors.Is(err, siw.ErrDomainMismatch) {
		t.Fatalf("got %v, want a domain mismatch", err)
	}
}

func TestVerifySignedMessage_refusesAnExpiredMessage(t *testing.T) {
	s := &Service{}
	address, sign := ethWallet(t)
	stale := time.Now().UTC().Add(-2 * ChallengeTTL).Truncate(time.Second)
	message := issuedMessage(t, siw.Ethereum, address, "acme", stale)

	_, err := s.VerifySignedMessage(context.Background(), message, sign(message), testDomain)
	if !errors.Is(err, siw.ErrExpired) {
		t.Fatalf("got %v, want an expiry refusal", err)
	}
}

// The signature covers the exact bytes. Every field a caller might want to
// change after the user approved it is one the signature no longer matches.
func TestVerifySignedMessage_refusesATamperedMessage(t *testing.T) {
	s := &Service{}
	address, sign := ethWallet(t)
	issuedAt := time.Now().UTC().Truncate(time.Second)
	message := issuedMessage(t, siw.Ethereum, address, "acme", issuedAt)
	signature := sign(message)

	for _, tc := range []struct {
		name     string
		tampered string
	}{
		{"the namespace in the resource", strings.Replace(message, "urn:orama:namespace:acme", "urn:orama:namespace:victim", 1)},
		{"the namespace in the statement", strings.Replace(message, "the acme namespace", "the victim namespace", 1)},
		{"the nonce", strings.Replace(message, "0123456789abcdef0123456789abcdef", "fedcba9876543210fedcba9876543210", 1)},
		{"the expiry", strings.Replace(message,
			issuedAt.Add(ChallengeTTL).Format(siw.TimeFormat),
			issuedAt.Add(24*time.Hour).Format(siw.TimeFormat), 1)},
		{"the domain", strings.Replace(message, testDomain+" wants", "evil.example wants", 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.tampered == message {
				t.Fatal("the tampering changed nothing, so this proves nothing")
			}
			host := testDomain
			if tc.name == "the domain" {
				host = "evil.example"
			}
			if _, err := s.VerifySignedMessage(context.Background(), tc.tampered, signature, host); err == nil {
				t.Error("a message altered after signing was accepted")
			}
		})
	}
}

func TestVerifySignedMessage_refusesAnotherWalletsSignature(t *testing.T) {
	s := &Service{}
	address, _ := ethWallet(t)
	_, signAsSomeoneElse := ethWallet(t)
	message := issuedMessage(t, siw.Ethereum, address, "acme", time.Now().UTC().Truncate(time.Second))

	if _, err := s.VerifySignedMessage(context.Background(), message, signAsSomeoneElse(message), testDomain); err == nil {
		t.Fatal("a signature from a different key logged in as this wallet")
	}
}

// The chain comes from the message, so presenting an Ethereum signature over a
// message that says Solana cannot pick the verification that would pass.
func TestVerifySignedMessage_theChainIsNotTheCallersToChoose(t *testing.T) {
	s := &Service{}
	solAddress, _ := solWallet(t)
	_, signAsEth := ethWallet(t)
	message := issuedMessage(t, siw.Solana, solAddress, "acme", time.Now().UTC().Truncate(time.Second))

	if _, err := s.VerifySignedMessage(context.Background(), message, signAsEth(message), testDomain); err == nil {
		t.Fatal("an Ethereum signature verified a Solana message")
	}
}

func TestVerifySignedMessage_refusesSomethingThatIsNotAMessage(t *testing.T) {
	s := &Service{}
	_, sign := ethWallet(t)
	for _, text := range []string{"", "0123456789abcdef", "not a sign-in message at all"} {
		if _, err := s.VerifySignedMessage(context.Background(), text, sign(text), testDomain); !errors.Is(err, ErrChallengeMessage) {
			t.Errorf("Parse(%q) = %v, want a refusal", text, err)
		}
	}
}

func TestNamespaceOf(t *testing.T) {
	base := &siw.Message{Resources: []string{namespaceResourcePrefix + "acme"}}
	if ns, err := NamespaceOf(base); err != nil || ns != "acme" {
		t.Errorf("got %q, %v", ns, err)
	}

	// A message this gateway did not issue carries no namespace, and guessing
	// one would mean signing the caller in somewhere the user never saw.
	if _, err := NamespaceOf(&siw.Message{Resources: []string{"ipfs://whatever"}}); err == nil {
		t.Error("a message naming no namespace was accepted")
	}
	if _, err := NamespaceOf(&siw.Message{Resources: []string{
		namespaceResourcePrefix + "acme", namespaceResourcePrefix + "victim",
	}}); err == nil {
		t.Error("a message naming two namespaces was accepted; one of them would have been picked")
	}
}

func TestParseChain(t *testing.T) {
	for in, want := range map[string]siw.Chain{
		"": siw.Ethereum, "ETH": siw.Ethereum, "eth": siw.Ethereum, "Ethereum": siw.Ethereum,
		"SOL": siw.Solana, "sol": siw.Solana, "solana": siw.Solana,
	} {
		got, err := ParseChain(in)
		if err != nil || got != want {
			t.Errorf("ParseChain(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := ParseChain("BTC"); err == nil {
		t.Error("an unsupported chain was accepted")
	}
}

// The grammar's nonce is 8*( ALPHA / DIGIT ). The nonce used to be base64url,
// whose '-' and '_' are not alphanumeric, so the message it went into was one a
// conforming wallet is entitled to refuse.
func TestGenerateNonce_isAlphanumericAndLong(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 64; i++ {
		nonce, err := generateNonce()
		if err != nil {
			t.Fatalf("generate: %v", err)
		}
		if len(nonce) < siw.MinNonceLength {
			t.Fatalf("nonce %q is shorter than the grammar allows", nonce)
		}
		for _, c := range nonce {
			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
				t.Fatalf("nonce %q is not alphanumeric", nonce)
			}
		}
		if seen[nonce] {
			t.Fatalf("nonce %q came up twice", nonce)
		}
		seen[nonce] = true
	}
}

func TestCanonicalAddress_checksumsAnEthereumAddress(t *testing.T) {
	lower := "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	got, err := canonicalAddress(siw.Ethereum, lower)
	if err != nil {
		t.Fatalf("canonicalAddress: %v", err)
	}
	if got != common.HexToAddress(lower).Hex() {
		t.Errorf("got %q, want the EIP-55 form", got)
	}
	// The grammar requires the checksummed form, so a message built from the
	// caller's spelling would not render at all.
	if _, err := canonicalAddress(siw.Ethereum, "not-an-address"); err == nil {
		t.Error("a non-address was accepted")
	}
}
