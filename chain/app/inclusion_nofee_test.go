package app_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protowire"

	abci "github.com/cometbft/cometbft/abci/types"

	"github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	sdk "github.com/cosmos/cosmos-sdk/types"
	txtypes "github.com/cosmos/cosmos-sdk/types/tx"
	signingtypes "github.com/cosmos/cosmos-sdk/types/tx/signing"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"

	"github.com/DeBrosOfficial/network/chain/app"
	"github.com/DeBrosOfficial/network/chain/x/inclusion"
)

// txWithFee is a transaction the SDK decoder accepts, with one message, one signer and one
// signature, whose auth info carries feeBytes as its fee field (nil leaves the fee out). The
// signature is garbage: nothing here verifies it.
func txWithFee(t testing.TB, feeBytes []byte) []byte {
	t.Helper()
	app.SetAddressPrefixes()
	key := secp256k1.GenPrivKey()
	from := sdk.AccAddress(key.PubKey().Address()).String()
	msg, err := types.NewAnyWithValue(&banktypes.MsgSend{FromAddress: from, ToAddress: from})
	require.NoError(t, err)
	pub, err := types.NewAnyWithValue(key.PubKey())
	require.NoError(t, err)
	body, err := (&txtypes.TxBody{Messages: []*types.Any{msg}}).Marshal()
	require.NoError(t, err)
	auth, err := (&txtypes.AuthInfo{SignerInfos: []*txtypes.SignerInfo{{
		PublicKey: pub,
		ModeInfo:  &txtypes.ModeInfo{Sum: &txtypes.ModeInfo_Single_{Single: &txtypes.ModeInfo_Single{Mode: signingtypes.SignMode_SIGN_MODE_DIRECT}}},
	}}}).Marshal()
	require.NoError(t, err)
	if feeBytes != nil {
		auth = protowire.AppendBytes(protowire.AppendTag(auth, 2, protowire.BytesType), feeBytes)
	}
	raw, err := (&txtypes.TxRaw{BodyBytes: body, AuthInfoBytes: auth, Signatures: [][]byte{make([]byte, 64)}}).Marshal()
	require.NoError(t, err)
	return raw
}

// txWithoutAFee leaves the fee out of the auth info.
func txWithoutAFee(t testing.TB) []byte { return txWithFee(t, nil) }

// txWithAnAmountlessFeeCoin states a fee of one coin that names norama and no amount: the SDK decodes
// it to a coin whose Int is nil.
func txWithAnAmountlessFeeCoin(t testing.TB) []byte {
	t.Helper()
	coin := protowire.AppendBytes(protowire.AppendTag(nil, 1, protowire.BytesType), []byte("norama"))
	return txWithFee(t, protowire.AppendBytes(protowire.AppendTag(nil, 1, protowire.BytesType), coin))
}

// A validator's vote extension that lists a transaction the SDK decodes but cannot read the fee of
// must be refused like any other bad list. The SDK's GetFee dereferences a missing fee, and
// reading the amount of a fee coin dereferences a nil Int; VerifyVoteExtension that panics on either
// is turned into an error by BaseApp, and CometBFT halts a node when the application returns an
// error from VerifyVoteExtension: one validator could stop every other. Found by
// FuzzClientTxDecode, whose empty transaction decodes and then panics when its fee is read, and by
// a review of what else reads a fee; FuzzVerifyVoteExtension holds the call to the rule.
func TestInclusion_verifyVoteExtensionRefusesATxWhoseFeeCannotBeRead(t *testing.T) {
	for name, tx := range map[string][]byte{
		"no fee":                    txWithoutAFee(t),
		"a fee coin with no amount": txWithAnAmountlessFeeCoin(t),
	} {
		t.Run(name, func(t *testing.T) {
			c := newInclusionChain(t, inclusionEnableHeight)
			_, err := c.app.TxConfig().TxDecoder()(tx)
			require.NoError(t, err, "the SDK decoder accepts the transaction")

			list, err := inclusion.EncodeList([][]byte{tx})
			require.NoError(t, err)
			resp, err := c.app.VerifyVoteExtension(&abci.RequestVerifyVoteExtension{
				Height: c.height + 1, Hash: make([]byte, 32), ValidatorAddress: c.consAddr(0), VoteExtension: list,
			})
			require.NoError(t, err, "VerifyVoteExtension must answer, not fail: a failure halts the node")
			require.Equal(t, abci.ResponseVerifyVoteExtension_REJECT, resp.Status)
		})
	}
}
