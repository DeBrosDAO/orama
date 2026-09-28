package types_test

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/codec/address"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/tx/signing"
	gogoproto "github.com/cosmos/gogoproto/proto"

	"github.com/DeBrosOfficial/network/chain/x/archive/types"
)

func TestAttachReplicasSignedByArchiver(t *testing.T) {
	signer := sdk.AccAddress(bytes.Repeat([]byte{9}, 20))
	msg := &types.MsgAttachReplicas{
		Archiver:    signer.String(),
		StartHeight: 1,
		EndHeight:   4,
		DealIds:     []string{"deal-1", "deal-2", "deal-3"},
	}
	require.Equal(t, []sdk.AccAddress{signer}, msg.GetSigners())

	ir, err := codectypes.NewInterfaceRegistryWithOptions(codectypes.InterfaceRegistryOptions{
		ProtoFiles: gogoproto.HybridResolver,
		SigningOptions: signing.Options{
			AddressCodec: address.Bech32Codec{
				Bech32Prefix: sdk.GetConfig().GetBech32AccountAddrPrefix(),
			},
			ValidatorAddressCodec: address.Bech32Codec{
				Bech32Prefix: sdk.GetConfig().GetBech32ValidatorAddrPrefix(),
			},
		},
	})
	require.NoError(t, err)
	types.RegisterInterfaces(ir)

	cdc := codec.NewProtoCodec(ir)
	signers, _, err := cdc.GetMsgV1Signers(msg)
	require.NoError(t, err)
	require.Equal(t, [][]byte{signer.Bytes()}, signers)

	attest := &types.MsgAttest{
		Archiver:    signer.String(),
		StartHeight: 1,
		EndHeight:   4,
		BundleCid:   "bafyarchivecid",
		BundleHash:  bytes.Repeat([]byte{1}, types.HashLen),
		MerkleRoot:  bytes.Repeat([]byte{2}, types.HashLen),
	}
	signers, _, err = cdc.GetMsgV1Signers(attest)
	require.NoError(t, err)
	require.Equal(t, [][]byte{signer.Bytes()}, signers)
}

func TestMsgServiceHasNoAdminMethod(t *testing.T) {
	cdc := codec.NewLegacyAmino()
	require.NotPanics(t, func() { types.RegisterLegacyAminoCodec(cdc) })
}
