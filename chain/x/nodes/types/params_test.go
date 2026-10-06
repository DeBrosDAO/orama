package types

import (
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
)

func init() {
	cfg := sdk.GetConfig()
	cfg.SetBech32PrefixForAccount(params.Bech32Prefix, params.Bech32PrefixAccPub)
	cfg.SetBech32PrefixForValidator(params.Bech32PrefixValAddr, params.Bech32PrefixValPub)
	cfg.SetBech32PrefixForConsensusNode(params.Bech32PrefixConsAddr, params.Bech32PrefixConsPub)
	cfg.SetCoinType(params.CoinType)
}

func TestDefaultParams_matchTheSpecPlaceholders(t *testing.T) {
	p := DefaultParams()
	require.NoError(t, p.Validate())
	require.Equal(t, DefaultUnbondingSeconds, p.UnbondingSeconds)
	require.Equal(t, int64(21*24*60*60), p.UnbondingSeconds)
	require.True(t, p.BondPerGib.Equal(DefaultBondPerGiB))
	require.True(t, DefaultDepositPerByte.MulRaw(1024).Equal(math.NewInt(69_999_616)))
	for _, role := range AllRoles() {
		got, err := p.MinBondFor(role)
		require.NoError(t, err)
		require.True(t, got.Equal(math.NewInt(params.NoramaPerOrama)), role.String())
	}
	backed, err := BackedCapacity(math.ZeroInt(), p)
	require.NoError(t, err)
	require.Equal(t, DefaultProbationCapacityBytes, backed)
	backed, err = BackedCapacity(math.NewInt(params.NoramaPerOrama), p)
	require.NoError(t, err)
	require.Equal(t, GiB, backed)
	require.Equal(t, uint32(31), CapacityClass(GiB))
}

func TestParamsValidate_rejectsBrokenFloors(t *testing.T) {
	p := DefaultParams()
	p.UnbondingSeconds = 0
	require.Error(t, p.Validate())

	p = DefaultParams()
	p.MinBond[0].Amount = math.ZeroInt()
	require.Error(t, p.Validate())

	p = DefaultParams()
	p.BondPerGib = math.ZeroInt()
	require.Error(t, p.Validate())
}

func TestProtoRoundTrip_preservesBonds(t *testing.T) {
	node := Node{
		NodeId:   "node-1",
		Operator: "orama1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq5k8k7y",
		Roles:    []Role{RoleStorage},
		HotKey:   "orama1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq5k8k7y",
		Bonds:    []RoleBond{{Role: RoleStorage, Amount: math.NewInt(1_500)}},
		Status:   NodeStatusRegistered,
	}
	bz, err := node.Marshal()
	require.NoError(t, err)
	var out Node
	require.NoError(t, out.Unmarshal(bz))
	require.Equal(t, node.NodeId, out.NodeId)
	require.Len(t, out.Bonds, 1)
	require.True(t, out.Bonds[0].Amount.Equal(math.NewInt(1_500)))

	msg := MsgBondNode{Operator: node.Operator, NodeId: node.NodeId, Role: RoleRelay, Amount: math.NewInt(9)}
	bz, err = msg.Marshal()
	require.NoError(t, err)
	var msgOut MsgBondNode
	require.NoError(t, msgOut.Unmarshal(bz))
	require.True(t, msgOut.Amount.Equal(math.NewInt(9)))
	require.Equal(t, RoleRelay, msgOut.Role)
}

func TestCodecRegistration(t *testing.T) {
	reg := codectypes.NewInterfaceRegistry()
	require.NotPanics(t, func() { RegisterInterfaces(reg) })
	cdc := codec.NewLegacyAmino()
	require.NotPanics(t, func() { RegisterLegacyAminoCodec(cdc) })
}

func TestValidateEndpoint_rejectsPrivateAndUserinfo(t *testing.T) {
	require.NoError(t, ValidateEndpoints([]string{"https://node.example:443"}, 1, 8))
	require.NoError(t, ValidateEndpoints([]string{"/dns4/node.example/tcp/443"}, 1, 8))
	require.Error(t, ValidateEndpoints([]string{"https://10.0.0.1/"}, 1, 8))
	require.Error(t, ValidateEndpoints([]string{"https://user:pass@node.example/"}, 1, 8))
	require.Error(t, ValidateEndpoints([]string{"http://127.0.0.1:80"}, 1, 8))
	require.Error(t, ValidateBaseDomain("10.0.0.1"))
	require.NoError(t, ValidateBaseDomain("example.com"))
}
