package app_test

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	cmted25519 "github.com/cometbft/cometbft/crypto/ed25519"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/log/v2"

	"github.com/cosmos/cosmos-sdk/baseapp"
	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/app"
	powertypes "github.com/DeBrosOfficial/network/chain/x/power/types"
)

const (
	productionChainID = "orama-1"
	stagenetChainID   = "orama-stagenet-1"
	devnetChainID     = "orama-devnet-1"
	localnetChainID   = "orama-localnet-1"
)

// plannedConsensus is what `orama setup --create-network` writes into a genesis: the finite block
// gas limit, CometBFT's default block size, vote extensions from height 2, ed25519 as the only
// validator key type and CometBFT's evidence defaults.
func plannedConsensus() *cmtproto.ConsensusParams {
	return &cmtproto.ConsensusParams{
		Block:     &cmtproto.BlockParams{MaxGas: 100_000_000, MaxBytes: 22_020_096},
		Evidence:  &cmtproto.EvidenceParams{MaxAgeNumBlocks: app.ConsensusEvidenceMaxAgeNumBlocks, MaxAgeDuration: time.Duration(app.ConsensusEvidenceMaxAgeDurationNanos), MaxBytes: app.ConsensusEvidenceMaxBytes},
		Validator: &cmtproto.ValidatorParams{PubKeyTypes: []string{cmted25519.KeyType}},
		Abci:      &cmtproto.ABCIParams{VoteExtensionsEnableHeight: 2},
	}
}

// initChainWith runs InitChain on a chain-id's app with every locked parameter at its plan value and
// the given consensus parameters.
func initChainWith(t *testing.T, chainID string, cp *cmtproto.ConsensusParams) error {
	t.Helper()
	app.SetAddressPrefixes()
	oramaApp := app.NewOramaApp(log.NewNopLogger(), dbm.NewMemDB(), true, simtestutil.EmptyAppOptions{}, baseapp.SetChainID(chainID))
	t.Cleanup(func() { require.NoError(t, oramaApp.Close()) })
	genState := oramaApp.DefaultGenesis()
	members := make([]powertypes.BootstrapMember, shieldedTestCommittee)
	for i := range members {
		name := fmt.Sprintf("consensus-committee-%d", i)
		members[i] = powertypes.BootstrapMember{
			OperatorAddress: sdk.AccAddress(secretKey(name).PubKey().Address()).String(),
			Moniker:         "committee",
			ConsensusPubkey: cmted25519.GenPrivKeyFromSecret([]byte(name)).PubKey().Bytes(),
		}
	}
	powerGen := powertypes.DefaultGenesisState()
	powerGen.BootstrapCommittee = members
	genState[powertypes.ModuleName] = oramaApp.AppCodec().MustMarshalJSON(powerGen)
	bz, err := json.Marshal(genState)
	require.NoError(t, err)
	_, err = oramaApp.InitChain(&abci.RequestInitChain{
		ChainId: chainID, InitialHeight: 1, Time: time.Unix(1_700_000_000, 0), AppStateBytes: bz, ConsensusParams: cp,
	})
	return err
}

func TestLockedGenesis_initChainRefusesUnboundedBlockGasOnProductionChainID(t *testing.T) {
	cp := plannedConsensus()
	cp.Block.MaxGas = -1
	err := initChainWith(t, productionChainID, cp)
	require.ErrorContains(t, err, "max_gas", "the refusal must name the consensus parameter")
}

func TestLockedGenesis_initChainAcceptsThePlannedConsensusParamsOnProductionChainID(t *testing.T) {
	require.NoError(t, initChainWith(t, productionChainID, plannedConsensus()))
}

func TestValidateConsensusParams_productionPinsEveryValue(t *testing.T) {
	for name, edit := range map[string]func(*cmtproto.ConsensusParams){
		"unlimited block gas":      func(cp *cmtproto.ConsensusParams) { cp.Block.MaxGas = -1 },
		"zero block gas":           func(cp *cmtproto.ConsensusParams) { cp.Block.MaxGas = 0 },
		"another finite block gas": func(cp *cmtproto.ConsensusParams) { cp.Block.MaxGas = 50_000_000 },
		"a smaller block":          func(cp *cmtproto.ConsensusParams) { cp.Block.MaxBytes = 1 << 20 },
		"a larger block":           func(cp *cmtproto.ConsensusParams) { cp.Block.MaxBytes = 100 << 20 },
		"unlimited block size":     func(cp *cmtproto.ConsensusParams) { cp.Block.MaxBytes = -1 },
		"vote extensions off":      func(cp *cmtproto.ConsensusParams) { cp.Abci.VoteExtensionsEnableHeight = 0 },
		"vote extensions at 5":     func(cp *cmtproto.ConsensusParams) { cp.Abci.VoteExtensionsEnableHeight = 5 },
		"no abci params":           func(cp *cmtproto.ConsensusParams) { cp.Abci = nil },
		"no block params":          func(cp *cmtproto.ConsensusParams) { cp.Block = nil },
		"a second validator key type": func(cp *cmtproto.ConsensusParams) {
			cp.Validator.PubKeyTypes = []string{"ed25519", "secp256k1"}
		},
		"no validator key type":      func(cp *cmtproto.ConsensusParams) { cp.Validator.PubKeyTypes = nil },
		"no validator params":        func(cp *cmtproto.ConsensusParams) { cp.Validator = nil },
		"a shorter evidence age":     func(cp *cmtproto.ConsensusParams) { cp.Evidence.MaxAgeNumBlocks = 10 },
		"a shorter evidence time":    func(cp *cmtproto.ConsensusParams) { cp.Evidence.MaxAgeDuration = time.Minute },
		"the old 48 h evidence time": func(cp *cmtproto.ConsensusParams) { cp.Evidence.MaxAgeDuration = 48 * time.Hour },
		"a longer evidence age":      func(cp *cmtproto.ConsensusParams) { cp.Evidence.MaxAgeNumBlocks *= 2 },
		"an evidence size of zero":   func(cp *cmtproto.ConsensusParams) { cp.Evidence.MaxBytes = 0 },
		"no evidence params":         func(cp *cmtproto.ConsensusParams) { cp.Evidence = nil },
		"no consensus params at all": nil,
	} {
		t.Run(name, func(t *testing.T) {
			cp := plannedConsensus()
			if edit == nil {
				cp = nil
			} else {
				edit(cp)
			}
			require.Error(t, app.ValidateConsensusParams(productionChainID, cp))
		})
	}
	require.NoError(t, app.ValidateConsensusParams(productionChainID, plannedConsensus()))
}

func TestValidateConsensusParams_testNetworksNeedAFiniteGasLimitABoundedBlockAndVoteExtensions(t *testing.T) {
	for _, chainID := range []string{stagenetChainID, devnetChainID} {
		t.Run(chainID, func(t *testing.T) {
			require.NoError(t, app.ValidateConsensusParams(chainID, plannedConsensus()))

			relaxed := plannedConsensus()
			relaxed.Block.MaxGas = 10_000_000
			relaxed.Block.MaxBytes = 4 << 20
			relaxed.Abci.VoteExtensionsEnableHeight = 7
			relaxed.Evidence = &cmtproto.EvidenceParams{MaxAgeNumBlocks: 50, MaxAgeDuration: time.Hour, MaxBytes: 1 << 10}
			require.NoError(t, app.ValidateConsensusParams(chainID, relaxed), "a test network may pick its own finite values and evidence window")

			for name, edit := range map[string]func(*cmtproto.ConsensusParams){
				"unlimited block gas":  func(cp *cmtproto.ConsensusParams) { cp.Block.MaxGas = -1 },
				"zero block gas":       func(cp *cmtproto.ConsensusParams) { cp.Block.MaxGas = 0 },
				"unlimited block size": func(cp *cmtproto.ConsensusParams) { cp.Block.MaxBytes = -1 },
				"an oversized block":   func(cp *cmtproto.ConsensusParams) { cp.Block.MaxBytes = 22_020_097 },
				"vote extensions off":  func(cp *cmtproto.ConsensusParams) { cp.Abci.VoteExtensionsEnableHeight = 0 },
				"no abci params":       func(cp *cmtproto.ConsensusParams) { cp.Abci = nil },
				"another key type":     func(cp *cmtproto.ConsensusParams) { cp.Validator.PubKeyTypes = []string{"secp256k1"} },
				"no validator params":  func(cp *cmtproto.ConsensusParams) { cp.Validator = nil },
			} {
				cp := plannedConsensus()
				edit(cp)
				require.Errorf(t, app.ValidateConsensusParams(chainID, cp), "%s must be refused", name)
			}
			require.Error(t, app.ValidateConsensusParams(chainID, nil), "missing consensus params")
		})
	}
}

func TestValidateConsensusParams_aLocalnetIsFree(t *testing.T) {
	cp := plannedConsensus()
	cp.Validator.PubKeyTypes = []string{"secp256k1"}
	cp.Block.MaxGas = -1
	cp.Block.MaxBytes = 100 << 20
	cp.Abci.VoteExtensionsEnableHeight = 0
	require.NoError(t, app.ValidateConsensusParams(localnetChainID, cp))
	require.NoError(t, app.ValidateConsensusParams(localnetChainID, nil))
}

func TestValidateConsensusParams_aChainIDThatOnlyContainsAClassWordIsProduction(t *testing.T) {
	cp := plannedConsensus()
	cp.Block.MaxGas = 10_000_000
	require.Error(t, app.ValidateConsensusParams("orama-1-stagenet-x", cp))
}

func TestValidateConsensusParams_namesEveryProblem(t *testing.T) {
	cp := plannedConsensus()
	cp.Block.MaxGas = -1
	cp.Block.MaxBytes = -1
	cp.Abci.VoteExtensionsEnableHeight = 0
	err := app.ValidateConsensusParams(stagenetChainID, cp)
	require.ErrorContains(t, err, "max_gas")
	require.ErrorContains(t, err, "max_bytes")
	require.ErrorContains(t, err, "vote_extensions_enable_height")
}

func TestValidateConsensusParams_namesTheEvidenceAndKeyTypeProblems(t *testing.T) {
	cp := plannedConsensus()
	cp.Validator.PubKeyTypes = []string{"secp256k1"}
	cp.Evidence = &cmtproto.EvidenceParams{MaxAgeNumBlocks: 1, MaxAgeDuration: time.Second, MaxBytes: 1}
	err := app.ValidateConsensusParams(productionChainID, cp)
	require.ErrorContains(t, err, "validator.pub_key_types")
	require.ErrorContains(t, err, "evidence.max_age_num_blocks")
	require.ErrorContains(t, err, "evidence.max_age_duration")
	require.ErrorContains(t, err, "evidence.max_bytes")
}
