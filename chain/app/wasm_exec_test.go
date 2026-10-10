//go:build cgo && !nowasm

package app_test

import (
	_ "embed"
	"encoding/json"
	"testing"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/stretchr/testify/require"

	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

//go:embed testdata/echo.wasm
var echoWasm []byte

// TestEchoContractInstantiateAndExecute runs a contract this repo built
// (chain/app/testdata/echo). It is not an upstream blob.
func TestEchoContractInstantiateAndExecute(t *testing.T) {
	require.NotEmpty(t, echoWasm)
	oramaApp := buildTestApp(t)
	gen, _ := buildGenesisState(t, oramaApp)
	state, err := json.Marshal(gen)
	require.NoError(t, err)

	genesisTime := time.Unix(1_700_000_000, 0)
	_, err = oramaApp.InitChain(&abci.RequestInitChain{
		ChainId:       testChainID,
		InitialHeight: 1,
		Time:          genesisTime,
		AppStateBytes: state,
	})
	require.NoError(t, err)
	_, err = oramaApp.FinalizeBlock(&abci.RequestFinalizeBlock{
		Height: 1,
		Time:   genesisTime.Add(2 * time.Second),
	})
	require.NoError(t, err)
	_, err = oramaApp.Commit()
	require.NoError(t, err)

	ctx := oramaApp.NewContext(true).
		WithBlockHeight(2).
		WithBlockTime(genesisTime.Add(4 * time.Second)).
		WithChainID(testChainID).
		WithEventManager(sdk.NewEventManager()).
		WithGasMeter(storetypes.NewInfiniteGasMeter())

	creator := sdk.AccAddress(bytesRepeat(0x11))
	oramaApp.AccountKeeper.SetAccount(ctx, oramaApp.AccountKeeper.NewAccountWithAddress(ctx, creator))

	ops := oramaApp.WasmContractKeeper()
	codeID, checksum, err := ops.Create(ctx, creator, echoWasm, nil)
	require.NoError(t, err)
	require.Equal(t, uint64(1), codeID)
	require.Len(t, checksum, 32)

	contract, _, err := ops.Instantiate(ctx, codeID, creator, nil, []byte("{}"), "echo", nil)
	require.NoError(t, err)
	require.NotEmpty(t, contract)

	_, err = ops.Execute(ctx, contract, creator, []byte("{}"), nil)
	require.NoError(t, err)
}

func bytesRepeat(b byte) []byte {
	raw := make([]byte, 20)
	for i := range raw {
		raw[i] = b
	}
	return raw
}
