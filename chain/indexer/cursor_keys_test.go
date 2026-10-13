package indexer

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/cockroachdb/pebble"
	"github.com/stretchr/testify/require"

	"github.com/cosmos/cosmos-sdk/types/bech32"

	"github.com/DeBrosOfficial/network/chain/app/params"
)

// operatorOf is a validator operator address of n bytes whose last byte is seed.
func operatorOf(t *testing.T, seed byte, n int) string {
	t.Helper()
	raw := make([]byte, n)
	raw[n-1] = seed
	s, err := bech32.ConvertAndEncode(params.Bech32PrefixValAddr, raw)
	require.NoError(t, err)
	return s
}

// putOwned writes an asset record and its owner index row, the way the follower does.
func putOwned(t *testing.T, store *Store, owner string, id []byte, tree uint64, leaf uint32) Asset {
	t.Helper()
	a := Asset{ID: hex.EncodeToString(id), State: AssetCompressed, TreeID: tree, LeafIndex: leaf, Owner: owner}
	raw, err := json.Marshal(a)
	require.NoError(t, err)
	loc := location(id, tree, leaf)
	require.NoError(t, store.db.Set(assetKey(loc), raw, pebble.Sync))
	require.NoError(t, store.db.Set(ownerKey(owner, loc), nil, pebble.Sync))
	return a
}

// putReward writes what validator credited account in epoch, the way the follower does.
func putReward(t *testing.T, store *Store, account string, epoch uint64, validator string) {
	t.Helper()
	raw, err := json.Marshal(AccountReward{Epoch: epoch, Validator: validator, Role: RoleDelegator, Amount: "1", ForceBonded: "0"})
	require.NoError(t, err)
	require.NoError(t, store.db.Set(rewardAcctKey(account, epoch, validator), raw, pebble.Sync))
	require.NoError(t, store.db.Set([]byte(keyRewardsFrom), be64(1), pebble.Sync))
}

func TestAssetCursor_roundTripsAndRefusesWhatStringNeverWrites(t *testing.T) {
	a := Asset{ID: hex.EncodeToString(assetID(3)), TreeID: 7, LeafIndex: 41}
	c, err := assetCursorOf(a)
	require.NoError(t, err)
	require.Len(t, c.String(), AssetCursorLen)
	require.Equal(t, location(assetID(3), 7, 41), c.bound(), "the cursor is the location that ends the owner index key")
	got, err := ParseAssetCursor(c.String())
	require.NoError(t, err)
	require.Equal(t, c, got)

	_, err = assetCursorOf(Asset{ID: "abcd"})
	require.Error(t, err, "an asset id is 32 bytes")

	for name, s := range map[string]string{
		"empty":             "",
		"short":             c.String()[:AssetCursorLen-1],
		"long":              c.String() + "A",
		"padded":            c.String()[:AssetCursorLen-2] + "==",
		"standard alphabet": strings.Repeat("+", AssetCursorLen),
		"space":             c.String()[:AssetCursorLen-1] + " ",
		"trailing bits":     strings.Repeat("A", AssetCursorLen-1) + "B",
		"a height cursor":   Cursor{Height: 3}.String(),
	} {
		_, err := ParseAssetCursor(s)
		require.Error(t, err, name)
	}
}

func TestRewardCursor_roundTripsAndPinsItsLengthBounds(t *testing.T) {
	c := RewardCursor{Epoch: 12, Validator: operatorOf(t, 5, 20)}
	got, err := ParseRewardCursor(c.String())
	require.NoError(t, err)
	require.Equal(t, c, got)

	shortest := RewardCursor{Epoch: 1, Validator: operatorOf(t, 1, 1)}
	require.Len(t, shortest.String(), RewardCursorMinLen, "the one-byte operator address is the shortest")
	longest := RewardCursor{Epoch: 1<<64 - 1, Validator: operatorOf(t, 1, maxAddressBytes)}
	require.Len(t, longest.String(), RewardCursorMaxLen, "the 255-byte operator address is the longest")
	for _, c := range []RewardCursor{shortest, longest} {
		got, err := ParseRewardCursor(c.String())
		require.NoError(t, err)
		require.Equal(t, c, got)
	}

	raw := func(epoch uint64, validator string) string {
		return base64.RawURLEncoding.EncodeToString(append(be64(epoch), validator...))
	}
	for name, s := range map[string]string{
		"empty":                "",
		"short":                c.String()[:RewardCursorMinLen-1],
		"too long":             strings.Repeat("A", RewardCursorMaxLen+1),
		"padded":               c.String() + "=",
		"standard alphabet":    strings.Repeat("+", RewardCursorMinLen),
		"epoch zero":           raw(0, c.Validator),
		"an account address":   raw(1, addr(t, 1)),
		"an uppercase valoper": raw(1, strings.ToUpper(c.Validator)),
		"a bad checksum":       raw(1, c.Validator[:len(c.Validator)-1]+"q"),
		"no validator":         raw(1, "oramavaloper1"),
		"a height cursor":      Cursor{Height: 3}.String(),
	} {
		_, err := ParseRewardCursor(s)
		require.Error(t, err, name)
	}
}

func TestOwnerAssetsAfter_walksTheListOnceWithoutRepeatsOrGaps(t *testing.T) {
	store := openStore(t, t.TempDir())
	defer store.Close()
	owner, other := addr(t, 1), addr(t, 2)
	for seed := byte(1); seed <= 12; seed++ {
		putOwned(t, store, owner, assetID(seed), 1, uint32(seed))
	}
	// The chain lets a creator mint an id again: one id at three locations, which sort by tree and leaf.
	putOwned(t, store, owner, assetID(40), 9, 2)
	putOwned(t, store, owner, assetID(40), 2, 100)
	putOwned(t, store, owner, assetID(40), 2, 3)
	putOwned(t, store, other, assetID(99), 1, 1)

	whole, err := store.OwnerAssets(owner, 1, MaxLimit)
	require.NoError(t, err)
	require.Len(t, whole, 15)

	for _, limit := range []int{1, 2, 4, 15, 16} {
		var walked []Asset
		var after *AssetCursor
		for range 40 {
			var page []Asset
			if after == nil {
				page, err = store.OwnerAssets(owner, 1, limit)
			} else {
				page, err = store.OwnerAssetsAfter(owner, *after, limit)
			}
			require.NoError(t, err)
			if len(page) == 0 {
				break
			}
			walked = append(walked, page...)
			c, cerr := assetCursorOf(page[len(page)-1])
			require.NoError(t, cerr)
			after = &c
		}
		require.Equal(t, whole, walked, "limit %d", limit)
	}

	past, err := store.OwnerAssetsAfter(owner, AssetCursor{ID: [hashSize]byte{0xff}}, 10)
	require.NoError(t, err)
	require.Empty(t, past, "nothing is after the last id")
	first, err := store.OwnerAssetsAfter(owner, AssetCursor{}, 3)
	require.NoError(t, err)
	require.Equal(t, whole[:3], first, "a position before the first id reads from the first")
	none, err := store.OwnerAssetsAfter(addr(t, 9), AssetCursor{}, 10)
	require.NoError(t, err)
	require.Empty(t, none, "an owner with no assets has none after any position")
}

func TestAccountRewardsAfter_walksNewestEpochFirstAcrossValidatorsOfOneEpoch(t *testing.T) {
	store := openStore(t, t.TempDir())
	defer store.Close()
	acct, other := addr(t, 1), addr(t, 2)
	vals := []string{operatorOf(t, 1, 20), operatorOf(t, 2, 20), operatorOf(t, 3, 20)}
	for epoch := uint64(1); epoch <= 6; epoch++ {
		for _, v := range vals {
			putReward(t, store, acct, epoch, v)
		}
	}
	putReward(t, store, other, 4, vals[0])

	_, whole, err := store.AccountRewards(acct, 1, MaxLimit)
	require.NoError(t, err)
	require.Len(t, whole, 18)
	require.Equal(t, uint64(6), whole[0].Epoch, "newest epoch first")

	for _, limit := range []int{1, 2, 4, 18, 19} {
		var walked []AccountReward
		var after *RewardCursor
		for range 40 {
			var page []AccountReward
			var from uint64
			if after == nil {
				from, page, err = store.AccountRewards(acct, 1, limit)
			} else {
				from, page, err = store.AccountRewardsAfter(acct, *after, limit)
			}
			require.NoError(t, err)
			require.Equal(t, uint64(1), from, "the first recorded epoch comes with every page")
			if len(page) == 0 {
				break
			}
			walked = append(walked, page...)
			last := page[len(page)-1]
			after = &RewardCursor{Epoch: last.Epoch, Validator: last.Validator}
		}
		require.Equal(t, whole, walked, "limit %d: a page may end between two validators of one epoch", limit)
	}

	_, past, err := store.AccountRewardsAfter(acct, RewardCursor{Epoch: 1, Validator: vals[0]}, 10)
	require.NoError(t, err)
	require.Empty(t, past, "nothing is older than the first epoch's first validator")
	_, head, err := store.AccountRewardsAfter(acct, RewardCursor{Epoch: 1 << 40, Validator: vals[0]}, 2)
	require.NoError(t, err)
	require.Equal(t, whole[:2], head, "an epoch past the head reads from the head")
}

// pagesOf follows next_cursor from route and returns the rows under name, the page bodies, and
// fails if a cursor page carries a page number.
func pagesOf(t *testing.T, api *API, route, name string, limit int) ([]any, []map[string]any) {
	t.Helper()
	var rows []any
	var bodies []map[string]any
	cursor := ""
	for range 100 {
		target := route + "?limit=" + string(rune('0'+limit))
		if cursor != "" {
			target += "&cursor=" + cursor
		}
		code, body := get(t, api, http.MethodGet, target)
		require.Equal(t, http.StatusOK, code, target)
		if cursor != "" {
			require.NotContains(t, body, "page", "a cursor page is not a numbered page")
		} else {
			require.Equal(t, 1.0, body["page"])
		}
		rows = append(rows, body[name].([]any)...)
		bodies = append(bodies, body)
		next, more := body["next_cursor"].(string)
		if !more {
			require.Less(t, len(body[name].([]any)), limit, "only a short page ends the list")
			return rows, bodies
		}
		cursor = next
	}
	t.Fatal("a list did not end")
	return nil, nil
}

func TestListsAPI_anOwnersAssetsAndAnAccountsRewardsPageByCursor(t *testing.T) {
	store := openStore(t, t.TempDir())
	defer store.Close()
	api := NewAPI(store, fixedTip{earliest: 1, latest: 9})
	owner := addr(t, 1)
	for seed := byte(1); seed <= 11; seed++ {
		putOwned(t, store, owner, assetID(seed), 1, 0)
		putReward(t, store, owner, uint64(seed), operatorOf(t, 1, 20))
	}

	assets, bodies := pagesOf(t, api, "/index/v1/cnft/owners/"+owner+"/assets", "assets", 4)
	require.Len(t, assets, 11)
	require.Len(t, uniq(idsOf(assets)), 11, "no asset twice")
	require.Len(t, bodies[0]["next_cursor"], AssetCursorLen)
	code, whole := get(t, api, http.MethodGet, "/index/v1/cnft/owners/"+owner+"/assets?limit=100")
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, whole["assets"], assets, "the cursor walk is the page-number list")
	require.NotContains(t, whole, "next_cursor", "a short page names no next page")

	rewards, bodies := pagesOf(t, api, "/index/v1/accounts/"+owner+"/rewards", "rewards", 4)
	require.Len(t, rewards, 11)
	require.Equal(t, 11.0, rewards[0].(map[string]any)["epoch"], "newest epoch first")
	require.Equal(t, owner, bodies[0]["address"])
	require.Equal(t, 1.0, bodies[len(bodies)-1]["recorded_from_epoch"], "every page says from where the index records")
	_, whole = get(t, api, http.MethodGet, "/index/v1/accounts/"+owner+"/rewards?limit=100")
	require.Equal(t, whole["rewards"], rewards)

	code, body := get(t, api, http.MethodGet, "/index/v1/cnft/owners/"+owner+"/assets?limit=11")
	require.Equal(t, http.StatusOK, code)
	require.Contains(t, body, "next_cursor", "a full page may be followed by more")
	code, body = get(t, api, http.MethodGet, "/index/v1/cnft/owners/"+addr(t, 9)+"/assets")
	require.Equal(t, http.StatusOK, code)
	require.Empty(t, body["assets"])
	require.NotContains(t, body, "next_cursor")
}

func idsOf(rows []any) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.(map[string]any)["id"].(string))
	}
	return out
}

func TestListsAPI_deepPageNumbersAndBadCursorsAreRefusedOnTheOwnerAndRewardLists(t *testing.T) {
	store := openStore(t, t.TempDir())
	defer store.Close()
	api := NewAPI(store, fixedTip{earliest: 1, latest: 9})
	owner := addr(t, 1)
	assetCur := AssetCursor{TreeID: 1}.String()
	rewardCur := RewardCursor{Epoch: 3, Validator: operatorOf(t, 1, 20)}.String()

	for _, tc := range []struct {
		route, good, other string
	}{
		{"/index/v1/cnft/owners/" + owner + "/assets", assetCur, rewardCur},
		{"/index/v1/accounts/" + owner + "/rewards", rewardCur, assetCur},
	} {
		code, _ := get(t, api, http.MethodGet, tc.route+"?page=10")
		require.Equal(t, http.StatusOK, code, "the last numbered page: %s", tc.route)
		code, _ = get(t, api, http.MethodGet, tc.route+"?cursor="+tc.good+"&limit=100")
		require.Equal(t, http.StatusOK, code, tc.route)
		for name, query := range map[string]string{
			"a page past the bound":   "page=11",
			"the old deepest page":    "page=1000&limit=100",
			"page with cursor":        "page=1&cursor=" + tc.good,
			"cursor repeated":         "cursor=" + tc.good + "&cursor=" + tc.good,
			"the other list's cursor": "cursor=" + tc.other,
			"a transaction cursor":    "cursor=" + Cursor{Height: 3}.String(),
			"cursor too short":        "cursor=" + tc.good[:20],
			"empty cursor":            "cursor=",
			"limit past the bound":    "limit=101",
			"an unknown parameter":    "offset=3",
		} {
			code, body := get(t, api, http.MethodGet, tc.route+"?"+query)
			require.Equal(t, http.StatusBadRequest, code, "%s %s", tc.route, name)
			require.Contains(t, body, "error")
		}
	}
	code, _ := get(t, api, http.MethodGet, "/index/v1/accounts/ORAMA1X/rewards?cursor="+rewardCur)
	require.Equal(t, http.StatusBadRequest, code)

	code, _ = get(t, api, http.MethodGet, "/index/v1/epochs/1/rewards?page=11")
	require.NotEqual(t, http.StatusBadRequest, code, "the rewards of an epoch keep their page bound")
	code, _ = get(t, api, http.MethodGet, "/index/v1/epochs/1/rewards?cursor="+rewardCur)
	require.Equal(t, http.StatusBadRequest, code, "only the lists an account can grow take a cursor")
}
