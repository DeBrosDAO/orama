package stategen

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	cmted25519 "github.com/cometbft/cometbft/crypto/ed25519"
	cmttypes "github.com/cometbft/cometbft/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/log/v2"

	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"
	sdk "github.com/cosmos/cosmos-sdk/types"
	genutiltypes "github.com/cosmos/cosmos-sdk/x/genutil/types"

	"github.com/DeBrosOfficial/network/chain/app"
	emissiontypes "github.com/DeBrosOfficial/network/chain/x/emission/types"
	powertypes "github.com/DeBrosOfficial/network/chain/x/power/types"
)

const (
	testChainID = "orama-localnet-stategen-1"
	// testAppToml is the part of an app.toml the generator reads. Pebble is the backend oramad uses.
	testAppToml = "pruning = \"nothing\"\napp-db-backend = \"pebbledb\"\niavl-cache-size = 10000\n"
	// testConfigToml is the part of a config.toml the generator reads: the database backend CometBFT's state goes to.
	testConfigToml = "db_backend = \"goleveldb\"\n"
)

var testGenesisTime = time.Unix(1_700_000_000, 0).UTC()

// newTestHome writes a home the generator can open: an app.toml and the genesis of a chain with one
// bootstrap committee member, the same genesis the app's own tests start from.
func newTestHome(t *testing.T) string {
	t.Helper()
	app.SetAddressPrefixes()
	home := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, configDir), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(home, configDir, appConfigFile), []byte(testAppToml), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(home, configDir, cometConfigFile), []byte(testConfigToml), 0o600))
	proto := app.NewOramaApp(log.NewNopLogger(), dbm.NewMemDB(), true, simtestutil.EmptyAppOptions{}, baseapp.SetChainID(testChainID))
	genState := app.NewDefaultGenesisState(proto)

	member := sdk.AccAddress(ed25519.GenPrivKeyFromSecret([]byte("stategen-member")).PubKey().Address())
	power := powertypes.DefaultGenesisState()
	power.Params.MinCommitteeSize = 1
	power.BootstrapCommittee = []powertypes.BootstrapMember{{
		OperatorAddress: member.String(),
		Moniker:         "stategen-test",
		ConsensusPubkey: cmted25519.GenPrivKeyFromSecret([]byte("stategen-consensus")).PubKey().Bytes(),
	}}
	genState[powertypes.ModuleName] = proto.AppCodec().MustMarshalJSON(power)
	emission := emissiontypes.DefaultGenesisState()
	emission.Params = emissiontypes.NewParams(2*time.Second, 1, true)
	genState[emissiontypes.ModuleName] = proto.AppCodec().MustMarshalJSON(emission)

	appState, err := json.Marshal(genState)
	require.NoError(t, err)
	genesis := genutiltypes.NewAppGenesisWithVersion(testChainID, appState)
	genesis.GenesisTime = testGenesisTime
	genesis.InitialHeight = 1
	params := cmttypes.DefaultConsensusParams()
	genesis.Consensus = &genutiltypes.ConsensusGenesis{Params: params}
	require.NoError(t, genesis.SaveAs(filepath.Join(home, configDir, "genesis.json")))
	return home
}
