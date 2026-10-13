package app_test

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	cmtprototypes "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	emissionkeeper "github.com/DeBrosOfficial/network/chain/x/emission/keeper"
	emissiontypes "github.com/DeBrosOfficial/network/chain/x/emission/types"
)

const faucetAppProductionChainID = "orama-1"

// TestFaucet_appDripCreatesAccountAndKeepsSupplyInvariant runs the faucet through the real app:
// real x/bank (so the recipient account is created and module accounts are blocked) and the real
// supply invariant.
func TestFaucet_appDripCreatesAccountAndKeepsSupplyInvariant(t *testing.T) {
	oramaApp := buildTestApp(t)
	genState, _ := buildGenesisState(t, oramaApp)

	emissionGenState := emissiontypes.DefaultGenesisState()
	emissionGenState.Params = emissiontypes.NewParams(24*time.Hour, 1, true)
	emissionGenState.Params.FaucetEnabled = true
	genState[emissiontypes.ModuleName] = oramaApp.AppCodec().MustMarshalJSON(emissionGenState)
	stateBytes, err := json.Marshal(genState)
	require.NoError(t, err)

	genesisTime := time.Unix(1_700_000_000, 0)
	_, err = oramaApp.InitChain(&abci.RequestInitChain{
		ChainId: testChainID, InitialHeight: 1, Time: genesisTime, AppStateBytes: stateBytes,
	})
	require.NoError(t, err)
	_, err = oramaApp.FinalizeBlock(&abci.RequestFinalizeBlock{Height: 1, Time: genesisTime.Add(time.Second)})
	require.NoError(t, err)
	_, err = oramaApp.Commit()
	require.NoError(t, err)

	header := cmtprototypes.Header{ChainID: testChainID, Time: genesisTime.Add(2 * time.Second)}
	ctx := sdk.NewContext(oramaApp.CommitMultiStore(), header, false, oramaApp.Logger())
	signer := sdk.AccAddress(bytes.Repeat([]byte{7}, 20))
	recipient := sdk.AccAddress(bytes.Repeat([]byte{8}, 20))
	amount := math.NewInt(5 * params.NoramaPerOrama)
	server := emissionkeeper.NewMsgServer(oramaApp.EmissionKeeper)

	require.Nil(t, oramaApp.AccountKeeper.GetAccount(ctx, recipient), "the recipient must not exist before the drip")
	_, err = server.Faucet(ctx, &emissiontypes.MsgFaucet{Signer: signer.String(), Recipient: recipient.String(), Amount: amount})
	require.NoError(t, err)

	require.NotNil(t, oramaApp.AccountKeeper.GetAccount(ctx, recipient), "the drip must create the recipient account")
	require.True(t, oramaApp.BankKeeper.GetBalance(ctx, recipient, params.BaseDenom).Amount.Equal(amount))
	detail, broken := oramaApp.EmissionKeeper.CheckSupplyInvariant(ctx)
	require.False(t, broken, detail)

	moduleAddr := authtypes.NewModuleAddress(emissiontypes.ModuleName)
	_, err = server.Faucet(ctx, &emissiontypes.MsgFaucet{Signer: signer.String(), Recipient: moduleAddr.String(), Amount: amount})
	require.ErrorIs(t, err, emissiontypes.ErrFaucetRecipient)

	_, err = server.Faucet(ctx.WithChainID(faucetAppProductionChainID), &emissiontypes.MsgFaucet{
		Signer: signer.String(), Recipient: signer.String(), Amount: amount,
	})
	require.ErrorIs(t, err, emissiontypes.ErrFaucetProduction)
}
