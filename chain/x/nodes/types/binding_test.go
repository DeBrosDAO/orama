package types

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"

	"github.com/stretchr/testify/require"

	secp256k1 "github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
)

func TestVerifyBinding_secp256k1AndEd25519(t *testing.T) {
	priv := secp256k1.GenPrivKey()
	pub := priv.PubKey().Bytes()
	msg := BindingSignBytes("orama-test", "orama1operator", "hot", pub)
	sig, err := priv.Sign(msg)
	require.NoError(t, err)
	require.NoError(t, VerifyBinding("orama-test", "orama1operator", Binding{
		Service: "hot", KeyType: KeyTypeSecp256k1, Pubkey: pub, Signature: sig,
	}))

	edPub, edPriv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	edSig := ed25519.Sign(edPriv, BindingSignBytes("orama-test", "orama1operator", "tor", edPub))
	require.NoError(t, VerifyBinding("orama-test", "orama1operator", Binding{
		Service: "tor", KeyType: KeyTypeEd25519, Pubkey: edPub, Signature: edSig,
	}))
}

func TestVerifyBinding_rejectsBadAndForeign(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	good := ed25519.Sign(priv, BindingSignBytes("orama-test", "orama1operator", "tor", pub))
	binding := Binding{Service: "tor", KeyType: KeyTypeEd25519, Pubkey: pub, Signature: append([]byte(nil), good...)}
	binding.Signature[len(binding.Signature)-1] ^= 0xff
	err = VerifyBinding("orama-test", "orama1operator", binding)
	require.ErrorIs(t, err, ErrInvalidBinding)

	foreign := ed25519.Sign(priv, BindingSignBytes("other-chain", "orama1operator", "tor", pub))
	err = VerifyBinding("orama-test", "orama1operator", Binding{
		Service: "tor", KeyType: KeyTypeEd25519, Pubkey: pub, Signature: foreign,
	})
	require.ErrorIs(t, err, ErrInvalidBinding)

	otherOp := ed25519.Sign(priv, BindingSignBytes("orama-test", "orama1someoneelse", "tor", pub))
	err = VerifyBinding("orama-test", "orama1operator", Binding{
		Service: "tor", KeyType: KeyTypeEd25519, Pubkey: pub, Signature: otherOp,
	})
	require.ErrorIs(t, err, ErrInvalidBinding)
}
