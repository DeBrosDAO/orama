package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/types/query"

	"github.com/DeBrosOfficial/network/chain/x/storage/keeper"
	"github.com/DeBrosOfficial/network/chain/x/storage/types"
)

func (f *fixture) putDeals(t *testing.T, count uint64) {
	t.Helper()
	for id := uint64(1); id <= count; id++ {
		require.NoError(t, f.Keeper.Deals.Set(f.Ctx, id, types.Deal{
			Id: id, Class: types.DealClass_DEAL_CLASS_PUBLIC_PIN, Escrow: math.ZeroInt(), PricePerEpoch: math.NewInt(int64(id)),
			Status: types.DealStatus_DEAL_STATUS_ACTIVE,
		}))
	}
}

func TestDeals_pagesNewestFirstWithReverse(t *testing.T) {
	f := newFixture(t)
	f.init(t, nil)
	f.putDeals(t, 5)

	var got []uint64
	var key []byte
	for pages := 0; pages < 10; pages++ {
		res, err := f.Query.Deals(f.Ctx, &types.QueryDealsRequest{Pagination: &query.PageRequest{Key: key, Limit: 2, Reverse: true}})
		require.NoError(t, err)
		require.LessOrEqual(t, len(res.Deals), 2)
		for _, d := range res.Deals {
			got = append(got, d.Id)
		}
		if key = res.Pagination.NextKey; key == nil {
			break
		}
	}
	require.Equal(t, []uint64{5, 4, 3, 2, 1}, got)
}

func TestDeals_pagesOldestFirstByDefault(t *testing.T) {
	f := newFixture(t)
	f.init(t, nil)
	f.putDeals(t, 3)

	res, err := f.Query.Deals(f.Ctx, &types.QueryDealsRequest{})
	require.NoError(t, err)
	require.Len(t, res.Deals, 3)
	require.Equal(t, uint64(1), res.Deals[0].Id)
	require.Equal(t, types.DealClass_DEAL_CLASS_PUBLIC_PIN, res.Deals[0].Class)
}

func TestDeals_emptyListIsEmptyNotAnError(t *testing.T) {
	f := newFixture(t)
	f.init(t, nil)
	res, err := f.Query.Deals(f.Ctx, &types.QueryDealsRequest{})
	require.NoError(t, err)
	require.NotNil(t, res.Deals)
	require.Empty(t, res.Deals)
}

func TestDeals_aPageIsBoundedAndPagesByKey(t *testing.T) {
	f := newFixture(t)
	f.init(t, nil)
	f.putDeals(t, 150)

	_, err := f.Query.Deals(f.Ctx, &types.QueryDealsRequest{Pagination: &query.PageRequest{Offset: 5}})
	require.Error(t, err, "an offset skips keys one by one, so the query refuses it")

	res, err := f.Query.Deals(f.Ctx, &types.QueryDealsRequest{Pagination: &query.PageRequest{Limit: 1 << 20, CountTotal: true}})
	require.NoError(t, err)
	require.Len(t, res.Deals, keeper.MaxDealsPerPage, "a page holds at most 100 deals")
	require.NotNil(t, res.Pagination.NextKey)
	require.Zero(t, res.Pagination.Total, "a total would walk every deal, so it is not computed")
}
