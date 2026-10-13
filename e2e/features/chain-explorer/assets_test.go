//go:build e2e_fleet

package chainexplorer

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
)

const (
	// unknownID is a counter value no run reaches: a collection, proposal, listing or code with it
	// does not exist.
	unknownID = 999999999
	// shieldedPool is x/shielded's module account (authtypes.NewModuleAddress("shielded"), which is
	// also the protocol's signer-less address). The explorer reads its balance and its transactions.
	shieldedPool = "orama12hms2jejlyt5tdnalles8vzp97srs3q7kg7yrq"
	// maxQueryPage is the most entries one paginated query may ask for.
	maxQueryPage = 100
)

// queryDoc is the `json=` request of a query: the document is the request in proto field names.
func queryDoc(doc string) url.Values { return url.Values{"json": {doc}} }

func page(limit int) string { return fmt.Sprintf(`"pagination":{"limit":%d}`, limit) }

// TestExplorerAssets_tokenReadsAreServed: the token pages' reads. The bank lists every denom in
// order with the native coin among them, a holder list of the native coin is at most the page asked
// for and every entry holds that denom, x/token's parameters are public, and a token that does not
// exist is a 404 as a typed answer, not a failure.
func TestExplorerAssets_tokenReadsAreServed(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	creator := c.Validator(t, c.Node(t, 0)).Address

	var supply struct {
		Supply []struct{ Denom, Amount string } `json:"supply"`
	}
	decode(t, read(t, "/query/cosmos.bank.v1beta1.Query/TotalSupply", queryDoc(`{`+page(maxQueryPage)+`}`)), &supply)
	native := false
	for i, coin := range supply.Supply {
		native = native || coin.Denom == chain.Denom
		if coin.Amount == "" || (i > 0 && supply.Supply[i-1].Denom >= coin.Denom) {
			t.Errorf("supply entry %+v is empty or out of denom order after %+v", coin, supply.Supply[max(i-1, 0)])
		}
	}
	if !native || len(supply.Supply) > maxQueryPage {
		t.Errorf("total supply lists %d denoms, native coin present %v, want it present and at most %d", len(supply.Supply), native, maxQueryPage)
	}

	var owners struct {
		DenomOwners []struct {
			Address string `json:"address"`
			Balance struct{ Denom, Amount string }
		} `json:"denom_owners"`
	}
	decode(t, read(t, "/query/cosmos.bank.v1beta1.Query/DenomOwners", queryDoc(`{"denom":"`+chain.Denom+`",`+page(2)+`}`)), &owners)
	if len(owners.DenomOwners) == 0 || len(owners.DenomOwners) > 2 {
		t.Errorf("%d holders of the native coin, asked for at most 2", len(owners.DenomOwners))
	}
	for _, o := range owners.DenomOwners {
		if o.Address == "" || o.Balance.Denom != chain.Denom {
			t.Errorf("holder %+v does not hold %s", o, chain.Denom)
		}
	}

	var params struct {
		Params struct {
			CreationFee string `json:"creation_fee"`
		} `json:"params"`
	}
	decode(t, read(t, "/query/orama.token.v1.Query/Params", nil), &params)
	if params.Params.CreationFee == "" {
		t.Errorf("x/token parameters carry no creation fee: %+v", params)
	}

	missing := "factory/" + creator + "/nosuchtoken"
	if resp := read(t, "/query/orama.token.v1.Query/Token", queryDoc(`{"denom":"`+missing+`"}`)); resp.Status != http.StatusNotFound {
		t.Errorf("the token %s: HTTP %d, want 404: %.200s", missing, resp.Status, resp.Body)
	}
	var frozen struct {
		Frozen bool `json:"frozen"`
	}
	decode(t, read(t, "/query/orama.token.v1.Query/Frozen", queryDoc(`{"denom":"`+missing+`","account":"`+creator+`"}`)), &frozen)
	if frozen.Frozen {
		t.Errorf("an account is frozen for a token that does not exist")
	}
}

// TestExplorerAssets_contractReadsAreServed: the contract pages' reads. The code list and a code's
// own record agree, a code or a contract that is not there is a 404, a page above the cap is a 400,
// and the queries that run a contract or answer a whole wasm binary are not served at all.
func TestExplorerAssets_contractReadsAreServed(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	account := c.Validator(t, c.Node(t, 0)).Address

	var codes struct {
		CodeInfos []struct {
			CodeID  string `json:"code_id"`
			Creator string `json:"creator"`
		} `json:"code_infos"`
	}
	decode(t, read(t, "/query/cosmwasm.wasm.v1.Query/Codes", queryDoc(`{`+page(maxQueryPage)+`}`)), &codes)
	if len(codes.CodeInfos) > maxQueryPage {
		t.Errorf("%d codes, asked for at most %d", len(codes.CodeInfos), maxQueryPage)
	}
	if len(codes.CodeInfos) > 0 {
		first := codes.CodeInfos[0]
		var info struct {
			CodeID  string `json:"code_id"`
			Creator string `json:"creator"`
		}
		decode(t, read(t, "/query/cosmwasm.wasm.v1.Query/CodeInfo", queryDoc(`{"code_id":`+first.CodeID+`}`)), &info)
		if info.CodeID != first.CodeID || info.Creator != first.Creator {
			t.Errorf("code %s is %+v in its own record and %+v in the list", first.CodeID, info, first)
		}
		var instances struct {
			Contracts []string `json:"contracts"`
		}
		decode(t, read(t, "/query/cosmwasm.wasm.v1.Query/ContractsByCode", queryDoc(`{"code_id":`+first.CodeID+`,`+page(5)+`}`)), &instances)
		if len(instances.Contracts) > 5 {
			t.Errorf("%d contracts, asked for at most 5", len(instances.Contracts))
		}
	}

	var created struct {
		ContractAddresses []string `json:"contract_addresses"`
	}
	decode(t, read(t, "/query/cosmwasm.wasm.v1.Query/ContractsByCreator", queryDoc(`{"creator_address":"`+account+`",`+page(5)+`}`)), &created)

	for _, tc := range []struct {
		method, doc string
		want        int
	}{
		{"CodeInfo", fmt.Sprintf(`{"code_id":%d}`, unknownID), http.StatusNotFound},
		{"AllContractState", `{"address":"` + account + `"}`, http.StatusNotFound},
		{"ContractInfo", `{"address":"` + account + `"}`, http.StatusNotFound},
		{"Codes", `{` + page(maxQueryPage+1) + `}`, http.StatusBadRequest},
		{"AllContractState", `{"address":"` + account + `",` + page(maxQueryPage+1) + `}`, http.StatusBadRequest},
		{"SmartContractState", `{"address":"` + account + `","query_data":"e30="}`, http.StatusNotFound},
		{"Code", `{"code_id":1}`, http.StatusNotFound},
	} {
		if resp := read(t, "/query/cosmwasm.wasm.v1.Query/"+tc.method, queryDoc(tc.doc)); resp.Status != tc.want {
			t.Errorf("GET /query/cosmwasm.wasm.v1.Query/%s %s: HTTP %d, want %d: %.200s", tc.method, tc.doc, resp.Status, tc.want, resp.Body)
		}
	}
}

// TestExplorerAssets_marketNftAndGovernanceReadsAreServed: the reads of the NFT, market and
// governance pages. The module parameters and enacted state are public, a record that was never made
// is a 404 on each module, and the NFT owner and asset routes of the indexer answer an owner with
// nothing as an empty list.
func TestExplorerAssets_marketNftAndGovernanceReadsAreServed(t *testing.T) {
	t.Parallel()
	requireIndexer(t)
	c := chain.New(t)
	account := c.Validator(t, c.Node(t, 0)).Address

	var params struct {
		Params struct {
			VotingPeriodSeconds string `json:"voting_period_seconds"`
		} `json:"params"`
	}
	decode(t, read(t, "/query/orama.houses.v1.Query/Params", nil), &params)
	if params.Params.VotingPeriodSeconds == "" {
		t.Errorf("x/houses parameters carry no voting period: %+v", params)
	}
	var enacted struct {
		Enacted json.RawMessage `json:"enacted"`
	}
	decode(t, read(t, "/query/orama.houses.v1.Query/Enacted", nil), &enacted)
	if len(enacted.Enacted) == 0 {
		t.Errorf("x/houses answers no enacted state")
	}

	for _, tc := range []struct{ method, doc string }{
		{"orama.houses.v1.Query/Proposal", fmt.Sprintf(`{"proposal_id":%d}`, unknownID)},
		{"orama.houses.v1.Query/Vote", fmt.Sprintf(`{"proposal_id":%d,"voter":"%s"}`, unknownID, account)},
		{"orama.houses.v1.Query/HouseBond", `{"address":"` + account + `"}`},
		{"orama.market.v1.Query/Listing", fmt.Sprintf(`{"id":%d}`, unknownID)},
		{"orama.market.v1.Query/Bid", fmt.Sprintf(`{"listing_id":%d,"bid_id":%d}`, unknownID, unknownID)},
		{"orama.cnft.v1.Query/Collection", fmt.Sprintf(`{"id":%d}`, unknownID)},
		{"orama.cnft.v1.Query/Tree", fmt.Sprintf(`{"id":%d}`, unknownID)},
	} {
		if resp := read(t, "/query/"+tc.method, queryDoc(tc.doc)); resp.Status != http.StatusNotFound {
			t.Errorf("GET /query/%s %s: HTTP %d, want 404: %.200s", tc.method, tc.doc, resp.Status, resp.Body)
		}
	}

	var owned struct {
		Assets []json.RawMessage `json:"assets"`
	}
	decode(t, read(t, "/index/cnft/owners/"+account+"/assets", url.Values{"limit": {fmt.Sprint(indexListLimit)}}), &owned)
	if len(owned.Assets) > indexListLimit {
		t.Errorf("%d assets, asked for at most %d", len(owned.Assets), indexListLimit)
	}
	if resp := read(t, "/index/cnft/assets/"+fmt.Sprintf("%064d", 1), nil); resp.Status != http.StatusNotFound {
		t.Errorf("an asset id nobody minted: HTTP %d, want 404: %.200s", resp.Status, resp.Body)
	}
}

// TestExplorerAssets_shieldedPoolReadsAreServed: the shielded page's reads. The module's parameters
// and the size of its note tree are public, the pool's account balance is a bank balance of the
// native coin, the index lists the transactions that named that account, and the pool query that
// would split the queue from the pool is withheld.
func TestExplorerAssets_shieldedPoolReadsAreServed(t *testing.T) {
	t.Parallel()
	requireIndexer(t)

	var params struct {
		Params struct {
			MaxActionsPerBundle uint32 `json:"max_actions_per_bundle"`
			UnshieldFloor       string `json:"unshield_floor"`
		} `json:"params"`
	}
	decode(t, read(t, "/query/orama.shielded.v1.Query/Params", nil), &params)
	if params.Params.MaxActionsPerBundle == 0 || params.Params.UnshieldFloor == "" {
		t.Errorf("x/shielded parameters %+v", params)
	}

	var tree struct {
		TreeSize       string `json:"tree_size"`
		NullifierCount string `json:"nullifier_count"`
		Anchors        string `json:"anchors"`
	}
	decode(t, read(t, "/query/orama.shielded.v1.Query/TreeState", nil), &tree)
	if tree.TreeSize == "" || tree.NullifierCount == "" || tree.Anchors == "" {
		t.Errorf("tree state %+v", tree)
	}

	var balance struct {
		Balance struct{ Denom, Amount string } `json:"balance"`
	}
	decode(t, read(t, "/query/cosmos.bank.v1beta1.Query/Balance", queryDoc(`{"address":"`+shieldedPool+`","denom":"`+chain.Denom+`"}`)), &balance)
	if balance.Balance.Denom != chain.Denom || balance.Balance.Amount == "" {
		t.Errorf("the pool account's balance %+v", balance)
	}

	var txs struct {
		Txs []json.RawMessage `json:"txs"`
	}
	decode(t, read(t, "/index/accounts/"+shieldedPool+"/txs", url.Values{"limit": {fmt.Sprint(indexListLimit)}}), &txs)
	if len(txs.Txs) > indexListLimit {
		t.Errorf("%d pool transactions, asked for at most %d", len(txs.Txs), indexListLimit)
	}

	if resp := read(t, "/query/orama.shielded.v1.Query/Pools", nil); resp.Status != http.StatusNotFound {
		t.Errorf("the pools query: HTTP %d, want 404 (withheld): %.200s", resp.Status, resp.Body)
	}
}
