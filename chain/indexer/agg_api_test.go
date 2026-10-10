package indexer

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// econAPI serves an index of 7 blocks of the economy chain: epochs 1 and 2 are closed.
func econAPI(t *testing.T) (*API, []fakeValidator) {
	t.Helper()
	chain, vals := econChain(t)
	chain.ledger.balances = map[string]int64{"bonded_tokens_pool": 600, "not_bonded_tokens_pool": 200, "fees": 300}
	addEconBlocks(t, chain, 1, 7)
	return NewAPI(index(t, chain, 1), fixedTip{earliest: 1, latest: 7}), vals
}

func list(t *testing.T, api *API, target, key string) []any {
	t.Helper()
	code, body := get(t, api, http.MethodGet, target)
	require.Equal(t, http.StatusOK, code, target)
	rows, ok := body[key].([]any)
	require.True(t, ok, "%s has no %q list: %v", target, key, body)
	return rows
}

func TestAPI_epochsArePagedNewestFirst(t *testing.T) {
	api, _ := econAPI(t)

	rows := list(t, api, "/index/v1/epochs?limit=1", "epochs")
	require.Len(t, rows, 1)
	require.Equal(t, 2.0, rows[0].(map[string]any)["epoch"])
	rows = list(t, api, "/index/v1/epochs?page=2&limit=1", "epochs")
	require.Equal(t, 1.0, rows[0].(map[string]any)["epoch"])
	require.Empty(t, list(t, api, "/index/v1/epochs?page=3&limit=1", "epochs"), "past the oldest epoch")
	require.Len(t, list(t, api, "/index/v1/epochs", "epochs"), 2)

	code, body := get(t, api, http.MethodGet, "/index/v1/epochs?page=2&limit=1")
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, 2.0, body["page"])
	require.Equal(t, 1.0, body["limit"])
}

func TestAPI_oneEpochAndItsValidators(t *testing.T) {
	api, _ := econAPI(t)

	code, body := get(t, api, http.MethodGet, "/index/v1/epochs/2")
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, 4.0, body["start_height"])
	require.Equal(t, 6.0, body["end_height"])
	require.Equal(t, "600", body["minted_validators"])
	require.Equal(t, "300", body["rewards_credited"])

	rows := list(t, api, "/index/v1/epochs/2/validators?limit=2", "validators")
	require.Len(t, rows, 2)
	require.Equal(t, 300.0, rows[0].(map[string]any)["comet_power"], "highest power first")
	rows = list(t, api, "/index/v1/epochs/2/validators?limit=2&page=2", "validators")
	require.Len(t, rows, 1)
	require.Equal(t, 100.0, rows[0].(map[string]any)["comet_power"])
}

func TestAPI_anUnknownEpochIsNotFound(t *testing.T) {
	api, _ := econAPI(t)
	for _, target := range []string{"/index/v1/epochs/3", "/index/v1/epochs/99/validators", "/index/v1/epochs/1000000"} {
		code, body := get(t, api, http.MethodGet, target)
		require.Equal(t, http.StatusNotFound, code, target)
		require.Equal(t, "not indexed", body["error"])
	}
}

func TestAPI_supplyIsOnePointPerEpoch(t *testing.T) {
	api, _ := econAPI(t)
	points := list(t, api, "/index/v1/supply", "points")
	require.Len(t, points, 2)
	newest := points[0].(map[string]any)
	require.Equal(t, 2.0, newest["epoch"])
	require.Equal(t, "600", newest["bonded"])
	require.Equal(t, "200", newest["unbonding"])
	require.Equal(t, "300", newest["earnings_pools"])
	require.Len(t, list(t, api, "/index/v1/supply?limit=1&page=2", "points"), 1)
}

func TestAPI_validatorsAndOneValidatorsHistory(t *testing.T) {
	api, vals := econAPI(t)

	rows := list(t, api, "/index/v1/validators", "validators")
	require.Len(t, rows, 3)
	top := rows[0].(map[string]any)
	require.Equal(t, vals[0].operator, top["operator"])
	require.Equal(t, "bonded", top["status"])
	require.Equal(t, 2.0, top["last_epoch"].(map[string]any)["epoch"])

	code, body := get(t, api, http.MethodGet, "/index/v1/validators/"+vals[2].operator)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, 100.0, body["comet_power"])
	require.Equal(t, 2.0, body["last_epoch"].(map[string]any)["epoch"])

	for kind, want := range map[string]int{"epochs": 2, "slashes": 0, "jails": 0} {
		got := list(t, api, "/index/v1/validators/"+vals[2].operator+"/"+kind+"?limit=5", kind)
		require.Len(t, got, want, kind)
	}
	require.Len(t, list(t, api, "/index/v1/validators/"+vals[2].operator+"/epochs?limit=1&page=2", "epochs"), 1)
}

func TestAPI_anUnknownValidatorIsNotFoundAndABadAddressIsRefused(t *testing.T) {
	api, vals := econAPI(t)
	stranger := newFakeValidator(t, 77, 1).operator
	for _, target := range []string{
		"/index/v1/validators/" + stranger,
		"/index/v1/validators/" + stranger + "/epochs",
		"/index/v1/validators/" + stranger + "/slashes",
		"/index/v1/validators/" + stranger + "/jails",
	} {
		code, body := get(t, api, http.MethodGet, target)
		require.Equal(t, http.StatusNotFound, code, target)
		require.Equal(t, "not indexed", body["error"])
	}
	for _, target := range []string{
		"/index/v1/validators/" + addr(t, 1),
		"/index/v1/validators/" + strings.ToUpper(vals[0].operator),
		"/index/v1/validators/notanoperator/epochs",
	} {
		code, _ := get(t, api, http.MethodGet, target)
		require.Equal(t, http.StatusBadRequest, code, target)
	}
}

func TestAPI_longSeriesArePagedLikeEveryOtherList(t *testing.T) {
	api, _ := econAPI(t)
	for _, name := range []string{"hourly", "daily", "weekly"} {
		code, body := get(t, api, http.MethodGet, "/index/v1/stats/"+name+"?limit=1")
		require.Equal(t, http.StatusOK, code, name)
		require.Equal(t, map[string]string{"hourly": "hour", "daily": "day", "weekly": "week"}[name], body["interval"])
		require.Len(t, body["buckets"], 1)
	}
	code, _ := get(t, api, http.MethodGet, "/index/v1/stats/yearly")
	require.Equal(t, http.StatusNotFound, code)
}

func TestAPI_aggregateRoutesRefuseWhatTheOtherRoutesRefuse(t *testing.T) {
	api, vals := econAPI(t)
	op := vals[0].operator
	for _, target := range []string{
		"/index/v1/epochs?limit=0", "/index/v1/epochs?limit=101", "/index/v1/epochs?page=0", "/index/v1/epochs?page=1001",
		"/index/v1/epochs?page=1&page=2", "/index/v1/epochs?offset=1", "/index/v1/epochs?limit=",
		"/index/v1/epochs/0", "/index/v1/epochs/-1", "/index/v1/epochs/abc", "/index/v1/epochs/01", "/index/v1/epochs/1?x=1",
		"/index/v1/epochs/1/validators?limit=101", "/index/v1/epochs/x/validators",
		"/index/v1/supply?limit=101", "/index/v1/supply?x=1",
		"/index/v1/validators?page=1001", "/index/v1/validators/" + op + "?x=1", "/index/v1/validators/" + op + "/epochs?limit=0",
		"/index/v1/validators/" + op + "/slashes?page=0", "/index/v1/validators/" + op + "/jails?limit=101",
		"/index/v1/stats/daily?limit=101", "/index/v1/stats/weekly?x=1", "/index/v1/stats/hourly?page=1001",
	} {
		code, _ := get(t, api, http.MethodGet, target)
		require.Equal(t, http.StatusBadRequest, code, target)
	}
	for _, target := range []string{
		"/index/v1/epochs/", "/index/v1/epochs/1/other", "/index/v1/supply/1", "/index/v1/validators/" + op + "/other",
		"/index/v1/validators/" + op + "/epochs/1", "/index/v1/stats/daily/1",
	} {
		code, _ := get(t, api, http.MethodGet, target)
		require.Equal(t, http.StatusNotFound, code, target)
	}
	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/index/v1/epochs", nil))
	require.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

func TestAPI_aggregateListsAreEmptyBeforeAnyEpochCloses(t *testing.T) {
	chain, _ := econChain(t)
	chain.ledger.epochs(1 << 30)
	addEconBlocks(t, chain, 1, 2)
	api := NewAPI(index(t, chain, 1), fixedTip{earliest: 1, latest: 2})

	require.Empty(t, list(t, api, "/index/v1/epochs", "epochs"))
	require.Empty(t, list(t, api, "/index/v1/supply", "points"))
	require.Empty(t, list(t, api, "/index/v1/validators", "validators"))
}
