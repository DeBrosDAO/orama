package globalbind

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1" // #nosec G505 -- a Tor relay fingerprint is SHA-1 by definition
	"crypto/sha512"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/clusterreg"
)

func torSecret() (expanded []byte, pub ed25519.PublicKey) {
	seed := bytes.Repeat([]byte{0x42}, 32)
	sum := sha512.Sum512(seed)
	return sum[:], ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)
}

func rsaIdentity(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func identityDoc(t *testing.T, id RelayIdentity) map[string]string {
	t.Helper()
	raw, err := json.Marshal(id)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]string
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestRelayIdentity_roundTripsThroughTheNodesJSON(t *testing.T) {
	expanded, pub := torSecret()
	key := rsaIdentity(t)
	id, err := NewRelayIdentity(expanded, key, vectorChain, vectorOperator, "node-a", false)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(id)
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.ToUpper(hex.EncodeToString(id.Fingerprint)); !strings.Contains(string(raw), want) || len(want) != 40 {
		t.Errorf("the fingerprint is not 40 upper-case hex digits: %s", raw)
	}
	got, err := ParseRelayIdentity(raw, vectorChain, vectorOperator, "node-a", false)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Fingerprint, id.Fingerprint) || !bytes.Equal(got.Binding.Pubkey, pub) || got.Binding.Service != clusterreg.RelayService {
		t.Errorf("parsed %+v", got)
	}
	if !bytes.Equal(got.Binding.Signature, id.Binding.Signature) || !bytes.Equal(got.CrossCert, id.CrossCert) ||
		!bytes.Equal(got.RSAPublicKey, id.RSAPublicKey) || !bytes.Equal(got.RSASignature, id.RSASignature) {
		t.Error("signatures changed on the way")
	}
	if strings.Contains(string(raw), hex.EncodeToString(expanded)) || strings.Contains(string(raw), hex.EncodeToString(key.D.Bytes())) {
		t.Error("a secret is in the output")
	}
}

func TestParseRelayIdentity_refusals(t *testing.T) {
	expanded, _ := torSecret()
	key, other := rsaIdentity(t), rsaIdentity(t)
	id, err := NewRelayIdentity(expanded, key, vectorChain, vectorOperator, "node-a", false)
	if err != nil {
		t.Fatal(err)
	}
	otherID, err := NewRelayIdentity(expanded, other, vectorChain, vectorOperator, "node-a", false)
	if err != nil {
		t.Fatal(err)
	}
	wrongNode, err := NewRelayIdentity(expanded, key, vectorChain, vectorOperator, "someone-else", false)
	if err != nil {
		t.Fatal(err)
	}
	otherDoc, wrongNodeDoc := identityDoc(t, otherID), identityDoc(t, wrongNode)
	doc := identityDoc(t, id)
	cases := map[string]struct {
		edit             func(map[string]string)
		chain, node, who string
	}{
		"another chain":            {func(map[string]string) {}, "other-chain", "node-a", vectorOperator},
		"another node":             {func(map[string]string) {}, vectorChain, "node-b", vectorOperator},
		"another operator":         {func(map[string]string) {}, vectorChain, "node-a", "orama1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqnr0x9w"},
		"a lower-case fingerprint": {func(d map[string]string) { d["rsa_fingerprint"] = strings.ToLower(d["rsa_fingerprint"]) }, vectorChain, "node-a", vectorOperator},
		"a short fingerprint":      {func(d map[string]string) { d["rsa_fingerprint"] = d["rsa_fingerprint"][:38] }, vectorChain, "node-a", vectorOperator},
		"another service":          {func(d map[string]string) { d["service"] = "tor" }, vectorChain, "node-a", vectorOperator},
		"a secp256k1 key":          {func(d map[string]string) { d["key_type"] = KeyTypeSecp256k1 }, vectorChain, "node-a", vectorOperator},
		"a cut cross-certificate":  {func(d map[string]string) { d["cross_cert"] = d["cross_cert"][:126] }, vectorChain, "node-a", vectorOperator},
		"a cross-certificate of a different fingerprint": {func(d map[string]string) {
			d["cross_cert"] = otherDoc["cross_cert"]
		}, vectorChain, "node-a", vectorOperator},
		"the fingerprint of a key it does not hold": {func(d map[string]string) {
			d["rsa_fingerprint"], d["cross_cert"] = otherDoc["rsa_fingerprint"], otherDoc["cross_cert"]
		}, vectorChain, "node-a", vectorOperator},
		"another key's public half":      {func(d map[string]string) { d["rsa_public_key"] = otherDoc["rsa_public_key"] }, vectorChain, "node-a", vectorOperator},
		"another key's rsa signature":    {func(d map[string]string) { d["rsa_signature"] = otherDoc["rsa_signature"] }, vectorChain, "node-a", vectorOperator},
		"an rsa proof made for a node":   {func(d map[string]string) { d["rsa_signature"] = wrongNodeDoc["rsa_signature"] }, vectorChain, "node-a", vectorOperator},
		"no rsa public key":              {func(d map[string]string) { d["rsa_public_key"] = "" }, vectorChain, "node-a", vectorOperator},
		"an rsa public key that is text": {func(d map[string]string) { d["rsa_public_key"] = "zz" }, vectorChain, "node-a", vectorOperator},
		"a cut rsa signature":            {func(d map[string]string) { d["rsa_signature"] = d["rsa_signature"][:len(d["rsa_signature"])-2] }, vectorChain, "node-a", vectorOperator},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			d := map[string]string{}
			for k, v := range doc {
				d[k] = v
			}
			c.edit(d)
			edited, _ := json.Marshal(d)
			if _, err := ParseRelayIdentity(edited, c.chain, c.who, c.node, false); err == nil {
				t.Fatal("the identity was accepted")
			}
		})
	}
	if _, err := ParseRelayIdentity([]byte("not json"), vectorChain, vectorOperator, "node-a", false); err == nil {
		t.Error("text was accepted")
	}
	if _, err := NewRelayIdentity(expanded, nil, vectorChain, vectorOperator, "node-a", false); err == nil {
		t.Error("a relay with no RSA key was signed for")
	}
}

func TestVerifyRSAProof_aKeyOfTheWrongSizeIsRefused(t *testing.T) {
	for name, bits := range map[string]uint{"512 bits": 512, "4104 bits": 4104} {
		n := new(big.Int).Lsh(big.NewInt(1), bits-1)
		n.Add(n, big.NewInt(1))
		der := x509.MarshalPKCS1PublicKey(&rsa.PublicKey{N: n, E: 65537})
		sum := sha1.Sum(der) // #nosec G401
		err := verifyRSAProof(sum[:], der, make([]byte, bits/8), "node-a", make([]byte, 32), false)
		if err == nil || !strings.Contains(err.Error(), "bits") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

// Both signatures cover the exit declaration: an identity signed for a non-exit relay is refused as an
// exit and the reverse, and so is one whose cross-certificate or RSA proof alone comes from the other.
func TestParseRelayIdentity_exitIsBoundIntoBothSignatures(t *testing.T) {
	expanded, _ := torSecret()
	key := rsaIdentity(t)
	plain, err := NewRelayIdentity(expanded, key, vectorChain, vectorOperator, "node-a", false)
	if err != nil {
		t.Fatal(err)
	}
	exit, err := NewRelayIdentity(expanded, key, vectorChain, vectorOperator, "node-a", true)
	if err != nil {
		t.Fatal(err)
	}
	plainDoc, exitDoc := identityDoc(t, plain), identityDoc(t, exit)
	parse := func(d map[string]string, asExit bool) error {
		raw, _ := json.Marshal(d)
		_, err := ParseRelayIdentity(raw, vectorChain, vectorOperator, "node-a", asExit)
		return err
	}
	if err := parse(plainDoc, false); err != nil {
		t.Errorf("a non-exit identity was refused as a non-exit: %v", err)
	}
	if err := parse(exitDoc, true); err != nil {
		t.Errorf("an exit identity was refused as an exit: %v", err)
	}
	if parse(plainDoc, true) == nil {
		t.Error("a non-exit identity was accepted as an exit")
	}
	if parse(exitDoc, false) == nil {
		t.Error("an exit identity was accepted as a non-exit")
	}
	certOnly := map[string]string{}
	rsaOnly := map[string]string{}
	for k, v := range exitDoc {
		certOnly[k], rsaOnly[k] = v, v
	}
	certOnly["rsa_signature"] = plainDoc["rsa_signature"]
	rsaOnly["cross_cert"] = plainDoc["cross_cert"]
	if parse(certOnly, true) == nil {
		t.Error("an exit identity with a non-exit RSA proof was accepted")
	}
	if parse(rsaOnly, true) == nil {
		t.Error("an exit identity with a non-exit cross-certificate was accepted")
	}
}
