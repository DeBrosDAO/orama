//go:build e2e_fleet

package chainexplorer

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

const (
	chainPrefix = "/v1/chain"
	pollEvery   = 2 * time.Second
	pollBudget  = 2 * time.Minute

	maxBalanceDenoms = 100
	maxValidators    = 200
	hourlyBuckets    = 48
	indexListLimit   = 5
)

func read(t *testing.T, path string, query url.Values) *gw.Response {
	t.Helper()
	return harness.GW(t).MustSend(t, gw.Req{Path: chainPrefix + path, Query: query})
}

// readIndex is read for /v1/chain/index/...: a gateway proxies the indexer of its own node, so it
// goes to a gateway whose node has one (chain.Chain.IndexerGateway).
func readIndex(t *testing.T, path string, query url.Values) *gw.Response {
	t.Helper()
	return chain.New(t).IndexerGateway(t).MustSend(t, gw.Req{Path: chainPrefix + "/index" + path, Query: query})
}

// addressJSON is the `json=` request of a query on one address.
func addressJSON(field, address string) string {
	raw, _ := json.Marshal(map[string]string{field: address})
	return string(raw)
}

func decode(t *testing.T, resp *gw.Response, v any) {
	t.Helper()
	if resp.Status != http.StatusOK {
		t.Fatalf("HTTP %d: %.300s", resp.Status, resp.Body)
	}
	if err := json.Unmarshal(resp.Body, v); err != nil {
		t.Fatalf("the answer is not the expected JSON: %v: %.300s", err, resp.Body)
	}
}

// TestExplorerReads_balanceIsTheNodesBankBalance: the gateway's bank balances
// route answers the address's coins as the node's REST API gives them, so a key
// funded through the faucet holds exactly the funded amount of norama, and an
// address that holds nothing answers an empty list, not an error.
func TestExplorerReads_balanceIsTheNodesBankBalance(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	funded := chain.Orama(2)
	k := c.NewFundedKey(t, c.Node(t, 1), "e2e-explorer-balance", funded)

	var out struct {
		Balances []struct{ Denom, Amount string } `json:"balances"`
	}
	decode(t, read(t, "/query/cosmos.bank.v1beta1.Query/AllBalances", url.Values{"json": {addressJSON("address", k.Address)}}), &out)
	if len(out.Balances) != 1 || out.Balances[0].Denom != chain.Denom || out.Balances[0].Amount != funded.String() {
		t.Errorf("balances of %s: %+v, want %s %s", k.Address, out.Balances, funded.String(), chain.Denom)
	}
	if len(out.Balances) > maxBalanceDenoms {
		t.Errorf("%d balances, the route caps the page at %d", len(out.Balances), maxBalanceDenoms)
	}

	stranger := c.NewKey(t, c.Node(t, 1), "e2e-explorer-empty")
	decode(t, read(t, "/query/cosmos.bank.v1beta1.Query/AllBalances", url.Values{"json": {addressJSON("address", stranger.Address)}}), &out)
	if len(out.Balances) != 0 {
		t.Errorf("an unfunded key holds %+v", out.Balances)
	}
}

// TestExplorerReads_validatorsAreTheRunsBondedSet: the staking validators route
// lists the run's validators, each bonded with a moniker and an operator
// address, and the validator's own delegation (a self-bond) is readable by its
// account; its unbonding list is empty.
func TestExplorerReads_validatorsAreTheRunsBondedSet(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	var set struct {
		Validators []struct {
			OperatorAddress string `json:"operator_address"`
			Status          string `json:"status"`
			Jailed          bool   `json:"jailed"`
			Description     struct {
				Moniker string `json:"moniker"`
			} `json:"description"`
		} `json:"validators"`
	}
	decode(t, read(t, "/staking/validators", nil), &set)
	if len(set.Validators) != len(c.Nodes()) || len(set.Validators) > maxValidators {
		t.Fatalf("%d validators, want the run's %d", len(set.Validators), len(c.Nodes()))
	}
	valoper := c.Valoper(t, c.Validator(t, c.Node(t, 0)))
	found := false
	for _, v := range set.Validators {
		if v.Status != "BOND_STATUS_BONDED" || v.Description.Moniker == "" {
			t.Errorf("validator %+v is not bonded or has no moniker", v)
		}
		found = found || v.OperatorAddress == valoper
	}
	if !found {
		t.Errorf("validator %s is not in the list", valoper)
	}

	owner := c.Validator(t, c.Node(t, 0)).Address
	var delegations struct {
		DelegationResponses []struct {
			Delegation struct {
				ValidatorAddress string `json:"validator_address"`
			} `json:"delegation"`
		} `json:"delegation_responses"`
	}
	decode(t, read(t, "/query/cosmos.staking.v1beta1.Query/DelegatorDelegations", url.Values{"json": {addressJSON("delegator_addr", owner)}}), &delegations)
	if len(delegations.DelegationResponses) == 0 {
		t.Errorf("%s has no delegation", owner)
	}
	var unbonding struct {
		UnbondingResponses []json.RawMessage `json:"unbonding_responses"`
	}
	decode(t, read(t, "/query/cosmos.staking.v1beta1.Query/DelegatorUnbondingDelegations", url.Values{"json": {addressJSON("delegator_addr", owner)}}), &unbonding)
	if len(unbonding.UnbondingResponses) != 0 {
		t.Errorf("%s is unbonding %d entries on a run that unbonds nothing", owner, len(unbonding.UnbondingResponses))
	}
}

// TestExplorerReads_refusals: a page limit above 100 is refused (400) before the node sees it, a
// query the proxy does not serve is a 404, a caller query on the validator list is refused, and
// only GET is served.
func TestExplorerReads_refusals(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	addr := c.Validator(t, c.Node(t, 0)).Address
	for _, tc := range []struct {
		path  string
		query url.Values
		want  int
	}{
		{"/query/cosmos.bank.v1beta1.Query/AllBalances", url.Values{"json": {`{"address":"` + strings.ToUpper(addr) + `","pagination":{"limit":"1000"}}`}}, http.StatusBadRequest},
		{"/query/cosmos.bank.v1beta1.Query/TotalSupply", nil, http.StatusNotFound},
		{"/bank/balances/" + addr, nil, http.StatusNotFound},
		{"/staking/validators", url.Values{"pagination.limit": {"100000"}}, http.StatusBadRequest},
		{"/staking/validators", url.Values{"status": {"BOND_STATUS_UNBONDED"}}, http.StatusBadRequest},
	} {
		if resp := read(t, tc.path, tc.query); resp.Status != tc.want {
			t.Errorf("GET %s %v: HTTP %d, want %d: %.200s", tc.path, tc.query, resp.Status, tc.want, resp.Body)
		}
	}
	resp := harness.GW(t).MustSend(t, gw.Req{Method: http.MethodPost, Path: chainPrefix + "/staking/validators"})
	if resp.Status != http.StatusMethodNotAllowed {
		t.Errorf("POST /staking/validators: HTTP %d, want 405", resp.Status)
	}
}

// requireIndexer fails unless the gateway of a node with a chain indexer reaches it: the run's chain
// deploy installs one beside every node (e2e/scripts/chain-deploy.sh), stagenet's on one node
// (chain/scripts/stagenet/deploy.sh), so a 502 here is a fault.
func requireIndexer(t *testing.T) {
	t.Helper()
	if resp := readIndex(t, "/status", nil); resp.Status != http.StatusOK {
		t.Fatalf("/v1/chain/index/status answers HTTP %d: %.200s", resp.Status, resp.Body)
	}
}

// TestExplorerIndex_latestStatsAndAccount: the indexer's newest transactions,
// hourly statistics and account summary, through the gateway. A faucet drip
// is a transaction naming the key, so the key's summary counts it, the newest
// list holds transactions with a decoded body, and the statistics are 48 hourly
// buckets.
func TestExplorerIndex_latestStatsAndAccount(t *testing.T) {
	t.Parallel()
	requireIndexer(t)
	c := chain.New(t)
	k := c.NewFundedKey(t, c.Node(t, 1), "e2e-explorer-index", chain.Orama(1))

	var account struct {
		Address string `json:"address"`
		TxCount uint64 `json:"tx_count"`
	}
	eventually.Require(t, pollEvery, pollBudget, "the indexer to count the key's first transaction", func() (bool, error) {
		resp := readIndex(t, "/accounts/"+k.Address, nil)
		if resp.Status == http.StatusNotFound {
			return false, fmt.Errorf("not indexed yet")
		}
		if resp.Status != http.StatusOK {
			return false, eventually.Stop(fmt.Errorf("HTTP %d: %.200s", resp.Status, resp.Body))
		}
		return true, json.Unmarshal(resp.Body, &account)
	})
	if account.Address != k.Address || account.TxCount == 0 {
		t.Errorf("account summary %+v", account)
	}

	var latest struct {
		Txs []struct {
			Hash string            `json:"hash"`
			Body []json.RawMessage `json:"body"`
		} `json:"txs"`
	}
	decode(t, readIndex(t, "/txs", url.Values{"limit": {fmt.Sprint(indexListLimit)}}), &latest)
	if len(latest.Txs) == 0 || len(latest.Txs) > indexListLimit {
		t.Fatalf("%d newest transactions, want 1 to %d", len(latest.Txs), indexListLimit)
	}
	for _, tx := range latest.Txs {
		if len(tx.Hash) != 64 || len(tx.Body) == 0 {
			t.Errorf("transaction %+v has no hash or no decoded body", tx)
		}
	}

	var stats struct {
		Hours []struct {
			Txs uint64 `json:"txs"`
		} `json:"hours"`
	}
	decode(t, readIndex(t, "/stats", nil), &stats)
	if len(stats.Hours) != hourlyBuckets {
		t.Errorf("%d hourly buckets, want %d", len(stats.Hours), hourlyBuckets)
	}
}
