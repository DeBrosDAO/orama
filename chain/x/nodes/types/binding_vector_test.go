package types

import (
	"bytes"
	"encoding/hex"
	"testing"

	secp256k1 "github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
)

// bindingVectorSig is the signature core/pkg/globalbind.SignSecp256k1 produces
// for secret 0x11 repeated, chain orama-stagenet-2, the vector operator, and
// service "provider". Both sides must stay on it.
const bindingVectorSig = "222079d46ccc2e809b6e3cdb82b61cac8b70d7d45a1a87230dbe0c37f361f2ab3ec979ec4f3ff32139c71c2317dffd87df8b67f63c9389a6d0535be03b6530a6"

func TestBindingVector_matchesCoreSigner(t *testing.T) {
	secret := bytes.Repeat([]byte{0x11}, 32)
	priv := &secp256k1.PrivKey{Key: secret}
	pub := priv.PubKey().Bytes()
	const (
		chainID  = "orama-stagenet-2"
		operator = "orama19rl4cm2hmr8afy4kldpxz3fka4jguq0a5tup0s"
		service  = "provider"
	)
	sig, err := priv.Sign(BindingSignBytes(chainID, operator, service, pub))
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(sig) != bindingVectorSig {
		t.Fatalf("sig %x", sig)
	}
	if err := VerifyBinding(chainID, operator, Binding{
		Service: service, KeyType: KeyTypeSecp256k1, Pubkey: pub, Signature: sig,
	}); err != nil {
		t.Fatal(err)
	}
}
