package keeper_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	"github.com/DeBrosOfficial/network/chain/x/wasmpolicy/types"
)

// chunkOf builds a valid chunk whose addresses are written as given (a bech32 string is valid in
// lower or upper case, but not mixed).
func chunkOf(contract, payer string, seq uint64) types.DepositChunk {
	return types.DepositChunk{
		Contract: contract, Seq: seq, Payer: payer, Bytes: 4, PerByte: math.NewInt(1),
		DepositID: types.DepositID(contract, seq),
	}
}

func TestGenesisValidate_anUpperCasePayerIsTheSamePayer(t *testing.T) {
	gs := types.DefaultGenesisState()
	gs.DepositChunks = []types.DepositChunk{
		chunkOf(contractA.String(), alice.String(), 0),
		chunkOf(contractA.String(), strings.ToUpper(alice.String()), 1),
	}
	require.ErrorContains(t, gs.Validate(), "one payer is one chunk")
}

func TestGenesisValidate_anUpperCaseContractIsTheSameContract(t *testing.T) {
	gs := types.DefaultGenesisState()
	gs.DepositChunks = []types.DepositChunk{
		chunkOf(contractA.String(), alice.String(), 3),
		chunkOf(strings.ToUpper(contractA.String()), bob.String(), 3),
	}
	require.ErrorContains(t, gs.Validate(), "duplicate deposit chunk")

	gs.DepositChunks[1] = chunkOf(strings.ToUpper(contractA.String()), alice.String(), 4)
	require.ErrorContains(t, gs.Validate(), "one payer is one chunk", "the (contract, payer) key is canonical on both parts")
}

func TestGenesisValidate_distinctChunksStillPass(t *testing.T) {
	gs := types.DefaultGenesisState()
	gs.DepositChunks = []types.DepositChunk{
		chunkOf(contractA.String(), alice.String(), 0),
		chunkOf(contractA.String(), bob.String(), 1),
		chunkOf(contractB.String(), alice.String(), 0),
	}
	require.NoError(t, gs.Validate())
}

func TestGenesis_importRefusesAChunkWhoseX_feesDepositIsMissing(t *testing.T) {
	f := newFixture(t)
	require.NoError(t, f.k.ApplyStateDelta(f.ctx, contractA, alice, 100, 0))
	exported, err := f.k.ExportGenesis(f.ctx)
	require.NoError(t, err)

	empty := newFixture(t) // its x/fees holds no deposit for the chunk
	fresh := newFixture2(t, empty.fees)
	err = fresh.InitGenesis(fresh.ctx, exported)
	require.ErrorContains(t, err, "inconsistent")
	require.ErrorContains(t, err, types.DepositID(contractA.String(), 0))
}

func TestGenesis_exportRefusesALedgerThatDisagreesWithItsDeposits(t *testing.T) {
	f := newFixture(t)
	require.NoError(t, f.k.ApplyStateDelta(f.ctx, contractA, alice, 100, 0))
	_, err := f.k.ExportGenesis(f.ctx)
	require.NoError(t, err)

	delete(f.fees.deposits, types.DepositID(contractA.String(), 0))
	_, err = f.k.ExportGenesis(f.ctx)
	require.ErrorContains(t, err, "inconsistent")
}

func TestGenesis_aSoundEmptyLedgerImportsAndExports(t *testing.T) {
	f := newFixture(t)
	exported, err := f.k.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.Empty(t, exported.DepositChunks)
	fresh := newFixture2(t, f.fees)
	require.NoError(t, fresh.InitGenesis(fresh.ctx, exported))
}
