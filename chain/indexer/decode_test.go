package indexer

import (
	"testing"

	"github.com/stretchr/testify/require"

	gogoproto "github.com/cosmos/gogoproto/proto"

	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"

	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	txtypes "github.com/cosmos/cosmos-sdk/types/tx"
)

// A signer info whose public key is an Any with no type unpacks to no key and no error. The first
// signer of such a transaction is none; before this was fixed the indexer dereferenced the nil key
// and crashed on the transaction (found by FuzzIndexerReadEnvelope).
func TestReadEnvelope_aPublicKeyWithNoTypeIsNoSigner(t *testing.T) {
	auth, err := gogoproto.Marshal(&txtypes.AuthInfo{SignerInfos: []*txtypes.SignerInfo{{PublicKey: &codectypes.Any{}}}})
	require.NoError(t, err)
	raw, err := gogoproto.Marshal(&txtypes.TxRaw{AuthInfoBytes: auth})
	require.NoError(t, err)

	env, err := readEnvelope(newCodec(), raw)
	require.NoError(t, err)
	require.Empty(t, env.signer)
}

// A secp256k1 key of the wrong length panics in the SDK's Address. A transaction the chain refused
// can carry one, and the indexer reads it: the envelope is refused with an error, not a crash.
func TestReadEnvelope_aMalformedPublicKeyIsAnErrorNotAPanic(t *testing.T) {
	cdc := newCodec()
	key, err := codectypes.NewAnyWithValue(&secp256k1.PubKey{Key: make([]byte, 5)})
	require.NoError(t, err)
	auth, err := gogoproto.Marshal(&txtypes.AuthInfo{SignerInfos: []*txtypes.SignerInfo{{PublicKey: key}}})
	require.NoError(t, err)
	raw, err := gogoproto.Marshal(&txtypes.TxRaw{AuthInfoBytes: auth})
	require.NoError(t, err)

	_, err = readEnvelope(cdc, raw)
	require.ErrorContains(t, err, "malformed signer public key")
}
