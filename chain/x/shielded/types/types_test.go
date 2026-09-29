package types_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	nodestypes "github.com/DeBrosOfficial/network/chain/x/nodes/types"
	"github.com/DeBrosOfficial/network/chain/x/shielded/bundle"
	"github.com/DeBrosOfficial/network/chain/x/shielded/types"
)

func TestMain(m *testing.M) {
	cfg := sdk.GetConfig()
	cfg.SetBech32PrefixForAccount(params.Bech32Prefix, params.Bech32PrefixAccPub)
	cfg.SetBech32PrefixForValidator(params.Bech32PrefixValAddr, params.Bech32PrefixValPub)
	os.Exit(m.Run())
}

func TestDefaultParamsAreValid(t *testing.T) {
	require.NoError(t, types.DefaultParams().Validate())
	require.NoError(t, types.DefaultGenesisState().Validate())
}

func TestParams_validate(t *testing.T) {
	mutations := map[string]func(*types.Params){
		"anchor window": func(p *types.Params) { p.AnchorWindowBlocks = 0 },
		"action gas":    func(p *types.Params) { p.ActionGas = 0 },
		"max actions":   func(p *types.Params) { p.MaxActionsPerBundle = 0 },
		"nullifier fee": func(p *types.Params) { p.NullifierFee = math.ZeroInt() },
		"floor":         func(p *types.Params) { p.UnshieldFloor = math.NewInt(-1) },
		"topup":         func(p *types.Params) { p.MaxFeeTopup = math.Int{} },
		"queue cap":     func(p *types.Params) { p.QueuePerAddressCap = math.ZeroInt() },
	}
	for name, mutate := range mutations {
		p := types.DefaultParams()
		mutate(&p)
		require.Error(t, p.Validate(), name)
	}
}

func TestParams_txGasIsActionGasTimesActions(t *testing.T) {
	p := types.DefaultParams()
	require.Equal(t, 3*types.DefaultActionGas, p.TxGas(3))
	require.Zero(t, p.TxGas(0))
}

func TestSignerlessAddressIsAFixedModuleAddress(t *testing.T) {
	a, b := types.SignerlessAddress(), types.SignerlessAddress()
	require.Equal(t, a, b)
	require.Len(t, a, 20)
}

func TestMsgShieldedTransfer_validateBasic(t *testing.T) {
	good := types.MsgShieldedTransfer{Signer: types.SignerlessAddress().String(), Bundle: []byte{1}}
	require.NoError(t, good.ValidateBasic())

	other := good
	other.Signer = sdk.AccAddress([]byte("someone_____________")).String()
	require.ErrorIs(t, other.ValidateBasic(), types.ErrSigner, "only the protocol address signs a signer-less transfer")
	other.Signer = "junk"
	require.Error(t, other.ValidateBasic())

	for name, b := range map[string][]byte{"empty": nil, "oversize": make([]byte, bundle.MaxBytes+1)} {
		bad := good
		bad.Bundle = b
		require.ErrorIs(t, bad.ValidateBasic(), types.ErrBundleSize, name)
	}
}

func TestSignedMsgs_validateBasic(t *testing.T) {
	alice := sdk.AccAddress([]byte("alice_______________")).String()
	require.NoError(t, (&types.MsgShield{Signer: alice, Bundle: []byte{1}}).ValidateBasic())
	require.NoError(t, (&types.MsgShieldEarnings{Signer: alice, Bundle: []byte{1}}).ValidateBasic())
	require.Error(t, (&types.MsgShield{Signer: "", Bundle: []byte{1}}).ValidateBasic())
	require.Error(t, (&types.MsgShieldEarnings{Signer: alice}).ValidateBasic())
}

func TestMsgUnshield_validateBasicPerTarget(t *testing.T) {
	alice := sdk.AccAddress([]byte("alice_______________"))
	base := types.MsgUnshield{Signer: alice.String(), Bundle: []byte{1}}
	val := sdk.ValAddress(alice).String()

	ok := map[string]func(*types.MsgUnshield){
		"bond": func(m *types.MsgUnshield) { m.Target, m.Validator = types.UnshieldTargetBond, val },
		"node bond": func(m *types.MsgUnshield) {
			m.Target, m.NodeId, m.Role = types.UnshieldTargetNodeBond, "node-1", nodestypes.RoleStorage
		},
		"fee topup": func(m *types.MsgUnshield) { m.Target = types.UnshieldTargetFeeTopup },
		"deposit":   func(m *types.MsgUnshield) { m.Target = types.UnshieldTargetDeposit },
		"contract":  func(m *types.MsgUnshield) { m.Target = types.UnshieldTargetContract },
	}
	for name, set := range ok {
		m := base
		set(&m)
		require.NoError(t, m.ValidateBasic(), name)
	}
	bad := map[string]func(*types.MsgUnshield){
		"no target":                  func(m *types.MsgUnshield) {},
		"unknown target":             func(m *types.MsgUnshield) { m.Target = 99 },
		"bond without a validator":   func(m *types.MsgUnshield) { m.Target = types.UnshieldTargetBond },
		"bond to an account address": func(m *types.MsgUnshield) { m.Target, m.Validator = types.UnshieldTargetBond, alice.String() },
		"node bond without a role":   func(m *types.MsgUnshield) { m.Target, m.NodeId = types.UnshieldTargetNodeBond, "node-1" },
		"node bond without a node":   func(m *types.MsgUnshield) { m.Target, m.Role = types.UnshieldTargetNodeBond, nodestypes.RoleStorage },
	}
	for name, set := range bad {
		m := base
		set(&m)
		require.ErrorIs(t, m.ValidateBasic(), types.ErrTarget, name)
	}
}

func TestCodec_registersEveryMessage(t *testing.T) {
	registry := codectypes.NewInterfaceRegistry()
	types.RegisterInterfaces(registry)
	cdc := codec.NewProtoCodec(registry)
	for _, msg := range []sdk.Msg{&types.MsgShieldedTransfer{}, &types.MsgShield{}, &types.MsgShieldEarnings{}, &types.MsgUnshield{}} {
		any, err := codectypes.NewAnyWithValue(msg)
		require.NoError(t, err)
		var out sdk.Msg
		require.NoError(t, cdc.UnpackAny(any, &out))
	}
	amino := codec.NewLegacyAmino()
	types.RegisterLegacyAminoCodec(amino)
}

func TestMsgUnshield_bindingCommitsToEverythingThatDecidesWhereTheValueGoes(t *testing.T) {
	alice := sdk.AccAddress([]byte("alice_______________"))
	bob := sdk.AccAddress([]byte("bob_________________"))
	base := types.MsgUnshield{Signer: alice.String(), Bundle: []byte{1}, Target: types.UnshieldTargetBond, Validator: sdk.ValAddress(alice).String()}
	want, err := base.Binding()
	require.NoError(t, err)
	again, _ := base.Binding()
	require.Equal(t, want, again, "the binding is deterministic")

	changes := map[string]func(*types.MsgUnshield){
		"signer":    func(m *types.MsgUnshield) { m.Signer = bob.String() },
		"target":    func(m *types.MsgUnshield) { m.Target = types.UnshieldTargetFeeTopup },
		"validator": func(m *types.MsgUnshield) { m.Validator = sdk.ValAddress(bob).String() },
		"node":      func(m *types.MsgUnshield) { m.NodeId = "node-9" },
		"role":      func(m *types.MsgUnshield) { m.Role = nodestypes.RoleStorage },
	}
	for name, change := range changes {
		m := base
		change(&m)
		got, err := m.Binding()
		require.NoError(t, err, name)
		require.NotEqual(t, want, got, "changing the %s must change what the signatures commit to", name)
	}

	bad := base
	bad.Validator = "nope"
	_, err = bad.Binding()
	require.ErrorIs(t, err, types.ErrTarget)
	bad = base
	bad.Signer = "nope"
	_, err = bad.Binding()
	require.Error(t, err)
}
