//go:build e2e_fleet

package chainexplorer

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
)

const (
	// unknownEpoch is an epoch no run reaches.
	unknownEpoch  = 999999999
	maxIndexLimit = 100
	maxIndexPage  = 1000
)

type bucket struct {
	Start  string `json:"start"`
	Txs    uint64 `json:"txs"`
	Failed uint64 `json:"failed"`
}

// TestExplorerAggregates_longSeriesAreNewestFirstAndBounded: the hourly, daily and weekly
// statistics the indexer keeps beyond the 48 hourly buckets answer a page of buckets, newest
// first, from the bucket of the newest block backwards, with a time that steps back by exactly
// their interval and no more than the page limit asked for.
func TestExplorerAggregates_longSeriesAreNewestFirstAndBounded(t *testing.T) {
	t.Parallel()
	requireIndexer(t)
	for name, step := range map[string]time.Duration{"hourly": time.Hour, "daily": 24 * time.Hour, "weekly": 7 * 24 * time.Hour} {
		var out struct {
			Interval string   `json:"interval"`
			Page     int      `json:"page"`
			Limit    int      `json:"limit"`
			Buckets  []bucket `json:"buckets"`
		}
		decode(t, read(t, "/index/stats/"+name, url.Values{"limit": {fmt.Sprint(indexListLimit)}}), &out)
		if len(out.Buckets) == 0 || len(out.Buckets) > indexListLimit || out.Page != 1 || out.Limit != indexListLimit {
			t.Fatalf("%s: %d buckets on page %d of limit %d, want 1 to %d on page 1", name, len(out.Buckets), out.Page, out.Limit, indexListLimit)
		}
		for i := 1; i < len(out.Buckets); i++ {
			prev, err1 := time.Parse(time.RFC3339, out.Buckets[i-1].Start)
			next, err2 := time.Parse(time.RFC3339, out.Buckets[i].Start)
			if err1 != nil || err2 != nil || prev.Sub(next) != step {
				t.Errorf("%s: buckets %q then %q are not %s apart", name, out.Buckets[i-1].Start, out.Buckets[i].Start, step)
			}
		}
	}
}

// TestExplorerAggregates_epochsSupplyAndValidators: the epoch, supply and validator routes answer
// lists the explorer pages through. A run's chain may not have closed an epoch yet, and then the
// lists are empty, not errors; every epoch it has closed is a consistent row whose single-epoch
// route gives the same row, whose validators and supply point exist, and the run's own validator is
// known to the index with its operator address.
func TestExplorerAggregates_epochsSupplyAndValidators(t *testing.T) {
	t.Parallel()
	requireIndexer(t)
	c := chain.New(t)

	var epochs struct {
		Epochs []struct {
			Epoch       uint64 `json:"epoch"`
			StartHeight int64  `json:"start_height"`
			EndHeight   int64  `json:"end_height"`
			MintedTotal string `json:"minted_total"`
		} `json:"epochs"`
	}
	decode(t, read(t, "/index/epochs", url.Values{"limit": {fmt.Sprint(indexListLimit)}}), &epochs)
	if len(epochs.Epochs) > indexListLimit {
		t.Fatalf("%d epochs, asked for at most %d", len(epochs.Epochs), indexListLimit)
	}
	for i, e := range epochs.Epochs {
		if e.EndHeight < e.StartHeight || e.MintedTotal == "" || (i > 0 && e.Epoch >= epochs.Epochs[i-1].Epoch) {
			t.Errorf("epoch row %+v is inconsistent or out of order", e)
		}
	}
	if len(epochs.Epochs) > 0 {
		checkClosedEpoch(t, epochs.Epochs[0].Epoch)
	}

	if resp := read(t, fmt.Sprintf("/index/epochs/%d", unknownEpoch), nil); resp.Status != http.StatusNotFound {
		t.Errorf("an epoch no run reaches: HTTP %d, want 404", resp.Status)
	}

	var validators struct {
		Validators []struct {
			Operator string `json:"operator"`
		} `json:"validators"`
	}
	valoper := c.Valoper(t, c.Validator(t, c.Node(t, 0)))
	var one struct {
		Operator string `json:"operator"`
		Status   string `json:"status"`
	}
	decode(t, read(t, "/index/validators/"+valoper, nil), &one)
	if one.Operator != valoper || one.Status != "bonded" {
		t.Errorf("validator %s as the index knows it: %+v, want it bonded", valoper, one)
	}
	for _, kind := range []string{"epochs", "slashes", "jails"} {
		if resp := read(t, "/index/validators/"+valoper+"/"+kind, url.Values{"limit": {"1"}}); resp.Status != http.StatusOK {
			t.Errorf("validator %s %s: HTTP %d: %.200s", valoper, kind, resp.Status, resp.Body)
		}
	}
	decode(t, read(t, "/index/validators", url.Values{"limit": {fmt.Sprint(indexListLimit)}}), &validators)
	if len(validators.Validators) > indexListLimit {
		t.Errorf("%d validators, asked for at most %d", len(validators.Validators), indexListLimit)
	}
}

// checkClosedEpoch reads what an epoch that has closed must have: its row, its validators and its
// supply point.
func checkClosedEpoch(t *testing.T, epoch uint64) {
	t.Helper()
	var row struct {
		Epoch uint64 `json:"epoch"`
	}
	decode(t, read(t, fmt.Sprintf("/index/epochs/%d", epoch), nil), &row)
	if row.Epoch != epoch {
		t.Errorf("epoch %d answers the row of epoch %d", epoch, row.Epoch)
	}
	var vals struct {
		Epoch      uint64            `json:"epoch"`
		Validators []json.RawMessage `json:"validators"`
	}
	decode(t, read(t, fmt.Sprintf("/index/epochs/%d/validators", epoch), url.Values{"limit": {fmt.Sprint(indexListLimit)}}), &vals)
	if vals.Epoch != epoch || len(vals.Validators) == 0 {
		t.Errorf("epoch %d lists %d validators for epoch %d, want its bonded set", epoch, len(vals.Validators), vals.Epoch)
	}
	var supply struct {
		Points []struct {
			Epoch       uint64 `json:"epoch"`
			TotalSupply string `json:"total_supply"`
		} `json:"points"`
	}
	decode(t, read(t, "/index/supply", url.Values{"limit": {fmt.Sprint(indexListLimit)}}), &supply)
	if len(supply.Points) == 0 || supply.Points[0].TotalSupply == "" {
		t.Errorf("a closed epoch %d and no supply point: %+v", epoch, supply.Points)
	}
}

// TestExplorerAggregates_refusals: the same bounds as every other index list are enforced at the
// gateway, a page past the oldest row is an empty list, and a route or value that is no route is a
// 404 before the indexer is asked.
func TestExplorerAggregates_refusals(t *testing.T) {
	t.Parallel()
	requireIndexer(t)
	c := chain.New(t)
	account := c.Validator(t, c.Node(t, 0)).Address
	for _, tc := range []struct {
		path  string
		query url.Values
		want  int
	}{
		{"/index/epochs", url.Values{"limit": {fmt.Sprint(maxIndexLimit + 1)}}, http.StatusBadRequest},
		{"/index/epochs", url.Values{"page": {fmt.Sprint(maxIndexPage + 1)}}, http.StatusBadRequest},
		{"/index/epochs", url.Values{"limit": {"0"}}, http.StatusBadRequest},
		{"/index/epochs", url.Values{"offset": {"1"}}, http.StatusBadRequest},
		{"/index/supply", url.Values{"limit": {fmt.Sprint(maxIndexLimit + 1)}}, http.StatusBadRequest},
		{"/index/validators", url.Values{"x": {"1"}}, http.StatusBadRequest},
		{"/index/stats/daily", url.Values{"limit": {fmt.Sprint(maxIndexLimit + 1)}}, http.StatusBadRequest},
		{"/index/epochs/0", nil, http.StatusNotFound},
		{"/index/epochs/abc/validators", nil, http.StatusNotFound},
		{"/index/validators/" + account, nil, http.StatusNotFound},
		{"/index/stats/yearly", nil, http.StatusNotFound},
		{fmt.Sprintf("/index/epochs/%d/validators", unknownEpoch), nil, http.StatusNotFound},
	} {
		if resp := read(t, tc.path, tc.query); resp.Status != tc.want {
			t.Errorf("GET %s %v: HTTP %d, want %d: %.200s", tc.path, tc.query, resp.Status, tc.want, resp.Body)
		}
	}
	var past struct {
		Epochs []any `json:"epochs"`
	}
	decode(t, read(t, "/index/epochs", url.Values{"page": {fmt.Sprint(maxIndexPage)}, "limit": {fmt.Sprint(maxIndexLimit)}}), &past)
	if len(past.Epochs) != 0 {
		t.Errorf("page %d of epochs holds %d rows", maxIndexPage, len(past.Epochs))
	}
}
