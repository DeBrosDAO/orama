package node

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/log/v2"
	dbm "github.com/cosmos/cosmos-db"

	errorsmod "cosmossdk.io/errors"
	"github.com/cosmos/cosmos-sdk/store/v2/rootmulti"
	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"

	"github.com/DeBrosOfficial/network/chain/x/storage/types"
)

// prunedVersionError is what a real multistore says for a version it pruned: it commits five
// versions, prunes the first three and asks for version 2. Pruning is asynchronous, so the test
// waits for the store to answer.
func prunedVersionError(t *testing.T) error {
	t.Helper()
	rs := rootmulti.NewStore(dbm.NewMemDB(), log.NewNopLogger())
	key := storetypes.NewKVStoreKey("a")
	rs.MountStoreWithDB(key, storetypes.StoreTypeIAVL, nil)
	require.NoError(t, rs.LoadLatestVersion())
	for i := 0; i < 5; i++ {
		rs.GetKVStore(key).Set([]byte("k"), []byte{byte(i)})
		rs.Commit()
	}
	require.NoError(t, rs.PruneStores(3))
	var err error
	require.Eventually(t, func() bool {
		_, err = rs.CacheMultiStoreWithVersion(2)
		return err != nil
	}, 10*time.Second, 10*time.Millisecond, "the store never pruned version 2")
	return err
}

// baseappQueryLog is the log of the ABCI answer baseapp.CreateQueryContext produces when the
// multistore cannot load a version: the same wrap, converted by the SDK's own QueryResult.
func baseappQueryLog(cause error, height, latest int64) (string, string, uint32) {
	err := errorsmod.Wrapf(sdkerrors.ErrNotFound, "failed to load state at height %d; %s (latest height: %d)", height, cause, latest)
	res := sdkerrors.QueryResult(err, false)
	return res.Codespace, res.Log, res.Code
}

func TestQueryError_StatePruned_aVersionTheRealStorePrunedIsRecognised(t *testing.T) {
	space, logText, code := baseappQueryLog(prunedVersionError(t), 2, 100)
	require.Equal(t, uint32(38), code, "baseapp answers with sdkerrors.ErrNotFound")

	srv := fakeRPC(t, map[string]any{"abci_query": abciAnswer(t, code, logText, nil)})
	defer srv.Close()
	c, err := Dial(srv.URL)
	require.NoError(t, err)
	var got types.QuerySlotResponse
	err = c.QueryAt(context.Background(), 2, "/orama.storage.v1.Query/Slot", &types.QuerySlotRequest{DealId: 1}, &got)

	var qe *QueryError
	require.ErrorAs(t, err, &qe)
	require.Equal(t, space, qe.Codespace)
	require.True(t, qe.StatePruned(), "log: %s", qe.Log)
	require.False(t, qe.NotFound(), "a pruned height is not a missing record")
}

func TestQueryError_StatePruned_aHeightBelowTheFirstCommitIsRecognised(t *testing.T) {
	_, logText, code := baseappQueryLog(fmt.Errorf("no commit info found"), 4, 900)
	qe := &QueryError{Codespace: sdkerrors.ErrNotFound.Codespace(), Code: code, Log: logText}
	require.True(t, qe.StatePruned(), "a node that state-synced has no state under its base height")
}

func TestQueryError_StatePruned_otherFailuresAreNot(t *testing.T) {
	_, prunedLog, _ := baseappQueryLog(prunedVersionError(t), 2, 100)
	notFound := sdkerrors.ErrKeyNotFound
	cases := map[string]*QueryError{
		"a missing record":           {Codespace: notFound.Codespace(), Code: notFound.ABCICode(), Log: "rpc error: code = NotFound desc = key not found"},
		"a height in the future":     {Codespace: "sdk", Code: sdkerrors.ErrInvalidHeight.ABCICode(), Log: "cannot query with height in the future; please provide a valid height: invalid height"},
		"another not found":          {Codespace: "sdk", Code: sdkerrors.ErrNotFound.ABCICode(), Log: "listing 7: not found"},
		"another module's code 38":   {Codespace: "listing", Code: sdkerrors.ErrNotFound.ABCICode(), Log: prunedLog},
		"the words in another code":  {Codespace: "sdk", Code: sdkerrors.ErrInvalidRequest.ABCICode(), Log: prunedLog},
		"the words not at the start": {Codespace: "sdk", Code: sdkerrors.ErrNotFound.ABCICode(), Log: "rpc: " + prunedLog},
		"a store that could not load for another reason": {
			Codespace: "sdk", Code: sdkerrors.ErrNotFound.ABCICode(),
			Log: "failed to load state at height 2; the disk is full (latest height: 100): not found",
		},
		"no log": {Codespace: "sdk", Code: sdkerrors.ErrNotFound.ABCICode()},
	}
	for name, qe := range cases {
		require.False(t, qe.StatePruned(), name)
	}
}
