package keeper_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/storage/keeper"
	"github.com/DeBrosOfficial/network/chain/x/storage/types"
)

func TestNoCancelMessage(t *testing.T) {
	reg := codectypes.NewInterfaceRegistry()
	reg.RegisterInterface(sdk.MsgInterfaceProtoName, (*sdk.Msg)(nil))
	types.RegisterInterfaces(reg)

	for _, impl := range reg.ListImplementations(sdk.MsgInterfaceProtoName) {
		require.NotContains(t, strings.ToLower(impl), "cancel")
	}

	got := map[string]bool{}
	for _, method := range types.Msg_serviceDesc.Methods {
		require.NotContains(t, strings.ToLower(method.MethodName), "cancel")
		got[method.MethodName] = true
	}
	require.Equal(t, map[string]bool{
		"CreateDeal":              true,
		"ExtendDeal":              true,
		"GrantDealAuthorization":  true,
		"RevokeDealAuthorization": true,
		"AcceptDeal":              true,
		"DeclineDeal":             true,
		"SubmitProofs":            true,
		"ReleaseReplica":          true,
	}, got, "the msg service is exactly the user lifecycle; protocol deals and cancellation are absent")

	amino := codec.NewLegacyAmino()
	types.RegisterLegacyAminoCodec(amino)

	router := baseapp.NewMsgServiceRouter()
	router.SetInterfaceRegistry(reg)
	types.RegisterMsgServer(router, keeper.NewMsgServer(keeper.Keeper{}))
	require.Nil(t, router.HandlerByTypeURL("/orama.storage.v1.MsgCancelDeal"))
	require.Nil(t, router.HandlerByTypeURL("/orama.storage.v1.MsgCancel"))
	require.NotNil(t, router.HandlerByTypeURL(sdk.MsgTypeURL(&types.MsgCreateDeal{})))
	require.NotNil(t, router.HandlerByTypeURL(sdk.MsgTypeURL(&types.MsgReleaseReplica{})))
}
