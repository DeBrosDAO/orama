package keeper_test

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/query"

	"github.com/DeBrosOfficial/network/chain/x/relay/keeper"
	"github.com/DeBrosOfficial/network/chain/x/relay/types"
)

func TestRelays_pagesEveryRelayInFingerprintOrder(t *testing.T) {
	f := newTestFixture(t)
	f.init(t, 1, []sdk.AccAddress{acc(1)}, nil)
	q := keeper.NewQueryServerImpl(f.Keeper)
	exitOf := map[string]bool{}
	for i, mark := range []byte{0x33, 0x11, 0x22} {
		rk := newRelayKey(t, "node-"+string(rune('a'+i)), mark, acc(2), "10.1.0.1")
		f.register(t, rk, mark == 0x22)
		exitOf[string(rk.fp)] = mark == 0x22
	}

	var fingerprints [][]byte
	var key []byte
	for pages := 0; pages < 5; pages++ {
		res, err := q.Relays(f.Ctx, &types.QueryRelaysRequest{Pagination: &query.PageRequest{Key: key, Limit: 2}})
		require.NoError(t, err)
		require.LessOrEqual(t, len(res.Relays), 2)
		for _, r := range res.Relays {
			fingerprints = append(fingerprints, r.RsaFingerprint)
			require.Equal(t, exitOf[string(r.RsaFingerprint)], r.Exit)
		}
		if key = res.Pagination.NextKey; key == nil {
			break
		}
	}
	require.Len(t, fingerprints, 3)
	require.True(t, bytes.Compare(fingerprints[0], fingerprints[1]) < 0 && bytes.Compare(fingerprints[1], fingerprints[2]) < 0, "fingerprint order")
}

func TestRelays_emptyListIsEmptyNotAnError(t *testing.T) {
	f := newTestFixture(t)
	res, err := keeper.NewQueryServerImpl(f.Keeper).Relays(f.Ctx, &types.QueryRelaysRequest{})
	require.NoError(t, err)
	require.NotNil(t, res.Relays)
	require.Empty(t, res.Relays)
}

func TestRelays_aPageIsBoundedAndPagesByKey(t *testing.T) {
	f := newTestFixture(t)
	q := keeper.NewQueryServerImpl(f.Keeper)

	_, err := q.Relays(f.Ctx, &types.QueryRelaysRequest{Pagination: &query.PageRequest{Offset: 5}})
	require.Error(t, err, "an offset skips keys one by one, so the query refuses it")

	for i := 0; i < keeper.MaxRelaysPerPage+50; i++ {
		fp := make([]byte, types.RSAFingerprintLen)
		binary.BigEndian.PutUint32(fp, uint32(i))
		require.NoError(t, f.Keeper.Relays.Set(f.Ctx, fp, types.Relay{NodeId: "n", RsaFingerprint: fp}))
	}
	res, err := q.Relays(f.Ctx, &types.QueryRelaysRequest{Pagination: &query.PageRequest{Limit: 1 << 20, CountTotal: true}})
	require.NoError(t, err)
	require.Len(t, res.Relays, keeper.MaxRelaysPerPage, "a page holds at most 100 relays")
	require.NotNil(t, res.Pagination.NextKey)
	require.Zero(t, res.Pagination.Total, "a total would walk every relay, so it is not computed")
}
