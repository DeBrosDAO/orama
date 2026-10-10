package app_test

import (
	stded25519 "crypto/ed25519"
	"testing"
	"time"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	emissiontypes "github.com/DeBrosOfficial/network/chain/x/emission/types"
	nodestypes "github.com/DeBrosOfficial/network/chain/x/nodes/types"
	powerkeeper "github.com/DeBrosOfficial/network/chain/x/power/keeper"
	powertypes "github.com/DeBrosOfficial/network/chain/x/power/types"
)

// TestApp_aValidatorCountsTowardTheOperatorOfTheNodeThatBindsItsConsensusKey proves the wiring from
// x/nodes into x/power's per-operator cap on the real app: a validator whose consensus key no node
// binds is unlinked, and registering a node that binds the key attributes it to that node's operator.
func TestApp_aValidatorCountsTowardTheOperatorOfTheNodeThatBindsItsConsensusKey(t *testing.T) {
	oramaApp := buildTestApp(t)
	genesisTime := time.Unix(1_700_000_000, 0)
	genState, _ := committeeGenesis(t, oramaApp, 1, 365)
	validatorKey, operatorKey := ed25519.GenPrivKey(), ed25519.GenPrivKey()
	validatorAcc := sdk.AccAddress(validatorKey.PubKey().Address())
	operator := sdk.AccAddress(operatorKey.PubKey().Address())
	addAuthAccount(t, oramaApp, genState, validatorKey)
	addAuthAccount(t, oramaApp, genState, operatorKey)
	initChain(t, oramaApp, genState, 2_000_000, genesisTime)
	finalize(t, oramaApp, 1, genesisTime.Add(2*time.Second))

	bond := math.NewInt(1_000_000_000)
	gas := uint64(300_000)
	credit := bond.Add(math.NewInt(int64(gas))).MulRaw(2)
	fundCtx := oramaApp.NewNextBlockContext(cmtproto.Header{Height: 2, Time: genesisTime.Add(4 * time.Second)})
	require.NoError(t, oramaApp.BankKeeper.MintCoins(fundCtx, emissiontypes.ModuleName, sdk.NewCoins(sdk.NewCoin(params.BaseDenom, credit.MulRaw(2)))))
	for _, addr := range []sdk.AccAddress{validatorAcc, operator} {
		require.NoError(t, oramaApp.FeesKeeper.CreditEarnings(fundCtx, emissiontypes.ModuleName, addr, sdk.NewCoin(params.BaseDenom, credit)))
	}
	writeCache(t, fundCtx)
	_, err := oramaApp.Commit()
	require.NoError(t, err)

	consPub, consPriv, err := stded25519.GenerateKey(nil)
	require.NoError(t, err)
	create, err := stakingtypes.NewMsgCreateValidator(
		sdk.ValAddress(validatorAcc).String(), &ed25519.PubKey{Key: consPub},
		sdk.NewCoin(params.BaseDenom, bond), stakingtypes.NewDescription("linked", "", "", "", ""),
		stakingtypes.NewCommissionRates(math.LegacyNewDecWithPrec(1, 1), math.LegacyNewDecWithPrec(2, 1), math.LegacyNewDecWithPrec(1, 2)),
		math.OneInt(),
	)
	require.NoError(t, err)
	resp := finalize(t, oramaApp, 3, genesisTime.Add(6*time.Second), signedTx(t, oramaApp, validatorKey, 2, gas, create))
	require.Zero(t, resp.TxResults[0].Code, "create-validator failed: %s", resp.TxResults[0].Log)

	queries := powerkeeper.NewQueryServerImpl(oramaApp.PowerKeeper)
	operatorOf := func() string {
		res, err := queries.ValidatorPower(oramaApp.NewContext(true), &powertypes.QueryValidatorPowerRequest{OperatorAddress: sdk.ValAddress(validatorAcc).String()})
		require.NoError(t, err)
		return res.Operator
	}
	require.Equal(t, powertypes.UnlinkedOperator, operatorOf(), "no node binds the key yet")

	hotPriv := secp256k1.GenPrivKey()
	hotSig, err := hotPriv.Sign(nodestypes.BindingSignBytes(testChainID, operator.String(), nodestypes.HotKeyService, hotPriv.PubKey().Bytes()))
	require.NoError(t, err)
	register := signedTx(t, oramaApp, operatorKey, 3, gas,
		&nodestypes.MsgRegisterOperator{Operator: operator.String()},
		&nodestypes.MsgRegisterNode{
			Operator: operator.String(), NodeId: "node-1", Roles: []nodestypes.Role{nodestypes.RoleValidator},
			HotKey: sdk.AccAddress(hotPriv.PubKey().Address()).String(),
			Bindings: []nodestypes.Binding{{
				Service: nodestypes.ConsensusService, KeyType: nodestypes.KeyTypeEd25519, Pubkey: consPub,
				Signature: stded25519.Sign(consPriv, nodestypes.BindingSignBytes(testChainID, operator.String(), nodestypes.ConsensusService, consPub)),
			}, {
				Service: nodestypes.HotKeyService, KeyType: nodestypes.KeyTypeSecp256k1, Pubkey: hotPriv.PubKey().Bytes(), Signature: hotSig,
			}},
			Endpoints: []string{"https://node.example:443"}, RegionHint: "eu-1",
		})
	resp = finalize(t, oramaApp, 4, genesisTime.Add(8*time.Second), register)
	require.Zero(t, resp.TxResults[0].Code, "register failed: %s", resp.TxResults[0].Log)

	require.Equal(t, operator.String(), operatorOf(), "the node's operator now answers for the validator")

	retire := signedTx(t, oramaApp, operatorKey, 4, gas, &nodestypes.MsgRetireNode{Operator: operator.String(), NodeId: "node-1"})
	resp = finalize(t, oramaApp, 5, genesisTime.Add(10*time.Second), retire)
	require.Zero(t, resp.TxResults[0].Code, "retire failed: %s", resp.TxResults[0].Log)
	require.Equal(t, powertypes.UnlinkedOperator, operatorOf(), "retiring the node unlinks the validator")
}
