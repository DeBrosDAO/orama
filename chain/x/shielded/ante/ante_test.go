package ante_test

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cosmos/cosmos-sdk/x/tx/signing"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/codec/address"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sigtypes "github.com/cosmos/cosmos-sdk/types/tx/signing"
	authtx "github.com/cosmos/cosmos-sdk/x/auth/tx"
	"github.com/cosmos/gogoproto/proto"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/x/shielded/ante"
	"github.com/DeBrosOfficial/network/chain/x/shielded/bundle"
	"github.com/DeBrosOfficial/network/chain/x/shielded/keeper"
	"github.com/DeBrosOfficial/network/chain/x/shielded/testutil"
	"github.com/DeBrosOfficial/network/chain/x/shielded/types"
)

var alice = sdk.AccAddress([]byte("alice_______________"))

var (
	txConfigOnce sync.Once
	sharedConfig client.TxConfig
)

// txConfig builds the tx config once: creating the interface registry is the slow part.
func txConfig(t *testing.T) client.TxConfig {
	t.Helper()
	txConfigOnce.Do(func() { sharedConfig = newTxConfig(t) })
	return sharedConfig
}

func newTxConfig(t *testing.T) client.TxConfig {
	t.Helper()
	registry, err := codectypes.NewInterfaceRegistryWithOptions(codectypes.InterfaceRegistryOptions{
		ProtoFiles: proto.HybridResolver,
		SigningOptions: signing.Options{
			AddressCodec:          address.Bech32Codec{Bech32Prefix: params.Bech32Prefix},
			ValidatorAddressCodec: address.Bech32Codec{Bech32Prefix: params.Bech32PrefixValAddr},
		},
	})
	require.NoError(t, err)
	types.RegisterInterfaces(registry)
	return authtx.NewTxConfig(codec.NewProtoCodec(registry), authtx.DefaultSignModes)
}

func emptyRoot() [bundle.NodeLen]byte {
	root, _ := testutil.Tree{}.EmptyRoot()
	return root
}

func transferMsg(seed byte, fee int64) *types.MsgShieldedTransfer {
	return &types.MsgShieldedTransfer{
		Signer: types.SignerlessAddress().String(),
		Bundle: testutil.Bundle{Seed: seed, ValueBalance: fee, Anchor: emptyRoot()}.Encode(1),
	}
}

func shieldMsg(seed byte) *types.MsgShield {
	return &types.MsgShield{
		Signer: alice.String(),
		Bundle: testutil.Bundle{Seed: seed, ValueBalance: -100, Anchor: emptyRoot()}.Encode(1),
	}
}

func buildTx(t *testing.T, tweak func(client.TxBuilder), msgs ...sdk.Msg) sdk.Tx {
	t.Helper()
	b := txConfig(t).NewTxBuilder()
	require.NoError(t, b.SetMsgs(msgs...))
	if tweak != nil {
		tweak(b)
	}
	return b.GetTx()
}

func gas(limit uint64) func(client.TxBuilder) {
	return func(b client.TxBuilder) { b.SetGasLimit(limit) }
}

// next is the end of a chain: it records that it was reached.
func next(reached *bool) sdk.AnteHandler {
	return func(ctx sdk.Context, _ sdk.Tx, _ bool) (sdk.Context, error) {
		*reached = true
		return ctx, nil
	}
}

func run(d sdk.AnteDecorator, ctx sdk.Context, tx sdk.Tx, simulate bool) (bool, error) {
	var reached bool
	_, err := d.AnteHandle(ctx, tx, simulate, next(&reached))
	return reached, err
}

// oneActionGas is the test genesis's action gas (10) for a one-action bundle.
const oneActionGas = 10

func signerless(e *testutil.Env) ante.SignerlessDecorator {
	return ante.NewSignerlessDecorator(e.Keeper)
}

func TestRoute_onlyASingleSignerlessTransferTakesTheSignerlessPath(t *testing.T) {
	var got string
	route := ante.Route(
		func(ctx sdk.Context, _ sdk.Tx, _ bool) (sdk.Context, error) { got = "signerless"; return ctx, nil },
		func(ctx sdk.Context, _ sdk.Tx, _ bool) (sdk.Context, error) { got = "normal"; return ctx, nil },
	)
	e := testutil.NewEnv(t, nil)
	cases := map[string]struct {
		tx   sdk.Tx
		want string
	}{
		"one transfer":         {buildTx(t, nil, transferMsg(1, 20)), "signerless"},
		"a shield":             {buildTx(t, nil, shieldMsg(1)), "normal"},
		"transfer with others": {buildTx(t, nil, transferMsg(1, 20), shieldMsg(2)), "normal"},
		"two transfers":        {buildTx(t, nil, transferMsg(1, 20), transferMsg(2, 20)), "normal"},
	}
	for name, c := range cases {
		_, err := route(e.Ctx, c.tx, false)
		require.NoError(t, err)
		require.Equal(t, c.want, got, name)
	}
}

func TestShape_shieldedMessagesMustBeAlone(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	_ = e
	cases := map[string]struct {
		tx      sdk.Tx
		refused bool
	}{
		"a lone shield":           {buildTx(t, nil, shieldMsg(1)), false},
		"two shields":             {buildTx(t, nil, shieldMsg(1), shieldMsg(2)), true},
		"a transfer among others": {buildTx(t, nil, transferMsg(1, 20), shieldMsg(2)), true},
		"a lone transfer":         {buildTx(t, nil, transferMsg(1, 20)), true},
	}
	for name, c := range cases {
		reached, err := run(ante.ShapeDecorator{}, e.Ctx, c.tx, false)
		if c.refused {
			require.ErrorIs(t, err, types.ErrTxShape, name)
			require.False(t, reached, name)
		} else {
			require.NoError(t, err, name)
			require.True(t, reached, name)
		}
	}
}

func TestSignerless_acceptsAWellFormedTransferAndVerifiesItOnce(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	ctx := e.CheckCtx().WithGasMeter(storetypes.NewGasMeter(oneActionGas))
	tx := buildTx(t, gas(oneActionGas), transferMsg(1, 20))

	reached, err := run(signerless(e), ctx, tx, false)
	require.NoError(t, err)
	require.True(t, reached)
	require.Equal(t, 1, e.V1.Calls)
	require.Equal(t, 1, e.V2.Calls)

	_, err = run(signerless(e), e.CheckCtx().WithGasMeter(storetypes.NewGasMeter(oneActionGas)), tx, false)
	require.ErrorIs(t, err, types.ErrNullifierSpent, "the first check marked the nullifier pending")
}

func TestSignerless_gasLimitMustBeExactlyTheBundlesGas(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	for _, limit := range []uint64{oneActionGas - 1, oneActionGas + 1, 0, 5_000_000} {
		ctx := e.CheckCtx().WithGasMeter(storetypes.NewGasMeter(limit))
		_, err := run(signerless(e), ctx, buildTx(t, gas(limit), transferMsg(1, 20)), false)
		require.ErrorIs(t, err, types.ErrTxShape, "limit %d", limit)
	}
	ctx := e.CheckCtx().WithGasMeter(storetypes.NewInfiniteGasMeter())
	_, err := run(signerless(e), ctx, buildTx(t, nil, transferMsg(1, 20)), true)
	require.NoError(t, err, "a simulation has no final gas limit yet")
}

func TestSignerless_refusesEverythingThatWouldIdentifyOrPaySomeone(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	pub := ed25519.GenPrivKey().PubKey()
	tweaks := map[string]func(client.TxBuilder){
		"a declared fee": func(b client.TxBuilder) { b.SetFeeAmount(sdk.NewCoins(sdk.NewInt64Coin(params.BaseDenom, 1))) },
		"a fee granter":  func(b client.TxBuilder) { b.SetFeeGranter(alice) },
		"a memo":         func(b client.TxBuilder) { b.SetMemo("hello") },
		"a signature": func(b client.TxBuilder) {
			_ = b.SetSignatures(sigtypes.SignatureV2{
				PubKey: pub,
				Data:   &sigtypes.SingleSignatureData{SignMode: sigtypes.SignMode_SIGN_MODE_DIRECT, Signature: []byte{1}},
			})
		},
	}
	for name, tweak := range tweaks {
		ctx := e.CheckCtx().WithGasMeter(storetypes.NewGasMeter(oneActionGas))
		tx := buildTx(t, func(b client.TxBuilder) { b.SetGasLimit(oneActionGas); tweak(b) }, transferMsg(1, 20))
		reached, err := run(signerless(e), ctx, tx, false)
		require.ErrorIs(t, err, types.ErrTxShape, name)
		require.False(t, reached, name)
	}
}

func TestSignerless_signerMustBeTheProtocolAddress(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	msg := transferMsg(1, 20)
	msg.Signer = alice.String()
	ctx := e.CheckCtx().WithGasMeter(storetypes.NewGasMeter(oneActionGas))
	_, err := run(signerless(e), ctx, buildTx(t, gas(oneActionGas), msg), false)
	require.ErrorIs(t, err, types.ErrSigner)
}

func TestSignerless_refusesWhatIsNotATransfer(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	_, err := run(signerless(e), e.CheckCtx(), buildTx(t, gas(oneActionGas), shieldMsg(1)), false)
	require.ErrorIs(t, err, types.ErrTxShape)
}

func TestSignerless_feeTooLowAndReplayAreRefusedBeforeAnyProofWork(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	ctx := e.CheckCtx().WithGasMeter(storetypes.NewGasMeter(oneActionGas))
	_, err := run(signerless(e), ctx, buildTx(t, gas(oneActionGas), transferMsg(1, 5)), false)
	require.ErrorIs(t, err, types.ErrFeeTooLow)
	require.Zero(t, e.V1.Calls+e.V2.Calls, "the cheap checks ran first")
}

func TestSignerless_inABlockTheMessageVerifiesNotTheAnteHandler(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	ctx := e.Ctx.WithGasMeter(storetypes.NewGasMeter(oneActionGas))
	reached, err := run(signerless(e), ctx, buildTx(t, gas(oneActionGas), transferMsg(1, 20)), false)
	require.NoError(t, err)
	require.True(t, reached)
	require.Zero(t, e.V1.Calls+e.V2.Calls, "verification happens once, in the message server")
	_, err = e.Keeper.Admit(e.Ctx, transferMsg(1, 20).Bundle, nil, keeper.KindTransfer, false)
	require.NoError(t, err, "the ante handler marked nothing in a block, so the message can still run")
}

func TestSignerless_recheckSkipsTheProofButMarksThePending(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	ctx := e.CheckCtx().WithIsReCheckTx(true).WithGasMeter(storetypes.NewGasMeter(oneActionGas))
	tx := buildTx(t, gas(oneActionGas), transferMsg(1, 20))
	_, err := run(signerless(e), ctx, tx, false)
	require.NoError(t, err)
	require.Zero(t, e.V1.Calls+e.V2.Calls)
	_, err = run(signerless(e), ctx, tx, false)
	require.ErrorIs(t, err, types.ErrNullifierSpent)
}

func TestProof_checkStateVerifiesAndMarksBlocksDoNot(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	d := ante.NewProofDecorator(e.Keeper)
	tx := buildTx(t, nil, shieldMsg(1))

	reached, err := run(d, e.Ctx, tx, false)
	require.NoError(t, err)
	require.True(t, reached)
	require.Zero(t, e.V1.Calls, "in a block the message server verifies")

	reached, err = run(d, e.CheckCtx(), tx, false)
	require.NoError(t, err)
	require.True(t, reached)
	require.Equal(t, 1, e.V1.Calls)
	_, err = run(d, e.CheckCtx(), tx, false)
	require.ErrorIs(t, err, types.ErrNullifierSpent, "a second mempool tx with the same nullifier")
}

func TestProof_aBadProofIsRefusedInTheMempool(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	e.V2.Reject = func([]byte) error { return testutil.ErrBoom }
	reached, err := run(ante.NewProofDecorator(e.Keeper), e.CheckCtx(), buildTx(t, nil, shieldMsg(1)), false)
	require.ErrorIs(t, err, testutil.ErrBoom)
	require.False(t, reached)
}
