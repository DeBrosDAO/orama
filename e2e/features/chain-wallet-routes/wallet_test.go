//go:build e2e_fleet

package chainwalletroutes

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math"
	"math/big"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

// None of these tests call t.Parallel: simulate and broadcast have per-address buckets on the
// gateway (burst 10 and 4), the whole package is one address, and TestRoutes_rateLimitsAreTheirOwn
// empties them on purpose, so it runs last.

const inclusionBudget = 2 * time.Minute

func txHashOf(raw []byte) string {
	sum := sha256.Sum256(raw)
	return strings.ToUpper(hex.EncodeToString(sum[:]))
}

// TestWalletRoutes_balanceSimulateBroadcastAndSeenOnChain is the wallet's whole path through the
// gateway alone: a fresh key funded by the faucet reads its balance and account, a signed
// delegation is simulated (nothing changes), broadcast, polled on /v1/chain/tx (404 and then 200,
// never 502) and the delegation is then visible through the same reads.
func TestWalletRoutes_balanceSimulateBroadcastAndSeenOnChain(t *testing.T) {
	c := chain.New(t)
	n := c.FaucetNode(t)
	g := gateway(t, c)
	funded := chain.Orama(10)
	k := c.NewFundedKey(t, n, "e2e-wallet-routes", funded)

	bal := queryOK(t, g, bank, "Balance", map[string]any{"address": k.Address, "denom": chain.Denom})
	if got := amountOf(t, bal, "balance"); got.Cmp(funded) != 0 {
		t.Errorf("gateway balance %s norama, want the faucet's %s", got.String(), funded.String())
	}
	all := queryOK(t, g, bank, "AllBalances", map[string]any{"address": k.Address, "pagination": map[string]any{"limit": maxPageLimit}})
	if list, _ := all["balances"].([]any); len(list) != 1 {
		t.Errorf("AllBalances of a faucet-funded key: %v", all)
	}
	spendable := queryOK(t, g, bank, "SpendableBalances", map[string]any{"address": k.Address})
	if list, _ := spendable["balances"].([]any); len(list) != 1 {
		t.Errorf("SpendableBalances of a faucet-funded key: %v", spendable)
	}
	info := queryOK(t, g, auth, "AccountInfo", map[string]any{"address": k.Address})
	if acct, _ := info["info"].(map[string]any); acct["address"] != k.Address || acct["sequence"] != "0" {
		t.Errorf("AccountInfo: %v", info)
	}
	// Account packs a BaseAccount in an Any; the gateway resolves it from its embedded descriptors.
	// account_number is a uint64 and so a JSON string.
	full := queryOK(t, g, auth, "Account", map[string]any{"address": k.Address})
	if acct, _ := full["account"].(map[string]any); acct["@type"] != "/cosmos.auth.v1beta1.BaseAccount" || acct["address"] != k.Address {
		t.Errorf("Account: %v", full)
	} else if number, ok := acct["account_number"].(string); !ok || number == "" {
		t.Errorf("Account account_number %v (%T), want a uint64 string", acct["account_number"], acct["account_number"])
	}
	queryOK(t, g, staking, "Params", nil)
	queryOK(t, g, staking, "Pool", nil)

	valoper := firstValoper(t, c)
	stake := chain.Orama(1)
	raw := c.EncodeTx(t, n, c.Sign(t, k, chain.TxOptions{}, delegateMsg(k.Address, valoper, stake)))
	hash := txHashOf(raw)

	// Nothing has been sent: the transaction is not found, a 404 to retry, never a 502 (bug 739).
	missing := g.MustSend(t, gw.Req{Path: "/v1/chain/tx", Query: map[string][]string{"hash": {hash}}})
	if missing.Status != http.StatusNotFound || missing.Header.Get("Retry-After") == "" {
		t.Fatalf("/v1/chain/tx of a transaction nobody sent: HTTP %d Retry-After %q: %s", missing.Status, missing.Header.Get("Retry-After"), missing.Body)
	}

	sim := simulate(t, g, raw)
	if sim.GasUsed == 0 || sim.GasWanted == 0 || sim.Fee.Denom != chain.Denom {
		t.Fatalf("simulate answered %+v", sim)
	}
	// gas_wanted is the limit the transaction declares, not the unlimited simulation meter's.
	if sim.GasWanted == math.MaxUint64 || sim.GasWanted < sim.GasUsed {
		t.Errorf("simulate gas_wanted %d with gas_used %d, want the transaction's own gas limit", sim.GasWanted, sim.GasUsed)
	}
	baseFee, _ := new(big.Int).SetString(sim.BaseFee, 10)
	want := new(big.Int).Mul(baseFee, new(big.Int).SetUint64(sim.GasUsed))
	if baseFee == nil || baseFee.Sign() <= 0 || sim.Fee.Amount != want.String() {
		t.Errorf("fee %s norama, want base fee %s x gas used %d = %s", sim.Fee.Amount, sim.BaseFee, sim.GasUsed, want)
	}
	// A simulate keeps nothing: the balance, the sequence and the transaction are as they were.
	if got := c.Bank(t, n, k.Address); got.Cmp(funded) != 0 {
		t.Errorf("a simulate changed the balance to %s norama", got.String())
	}
	if acct := queryOK(t, g, auth, "AccountInfo", map[string]any{"address": k.Address})["info"].(map[string]any); acct["sequence"] != "0" {
		t.Errorf("a simulate moved the sequence: %v", acct)
	}

	sent := postTx(t, g, "broadcast", raw).Expect(t, http.StatusOK)
	var ack struct {
		Code   uint32 `json:"code"`
		TxHash string `json:"tx_hash"`
	}
	if err := sent.Decode(&ack); err != nil {
		t.Fatal(err)
	}
	if ack.Code != 0 || ack.TxHash != hash {
		t.Fatalf("broadcast answered %+v, want code 0 and hash %s", ack, hash)
	}

	eventually.Require(t, chain.PollEvery, inclusionBudget, "/v1/chain/tx sees the broadcast transaction", func() (bool, error) {
		resp := g.MustSend(t, gw.Req{Path: "/v1/chain/tx", Query: map[string][]string{"hash": {hash}}})
		switch resp.Status {
		case http.StatusOK:
			return true, nil
		case http.StatusNotFound:
			return false, fmt.Errorf("not in a block yet (404, Retry-After %q)", resp.Header.Get("Retry-After"))
		default:
			return false, eventually.Stop(fmt.Errorf("/v1/chain/tx answered HTTP %d for a just-broadcast transaction, want 200 or a 404 to retry: %s", resp.Status, resp.Body))
		}
	})

	// The same bytes again: the mempool has seen them, or the sequence is spent. Either is the
	// chain's refusal, with the hash, never a 5xx.
	again := decodeRefusal(t, postTx(t, g, "broadcast", raw))
	if again.Codespace != "sdk" || (again.Code != 19 && again.Code != 32) || again.TxHash != hash {
		t.Errorf("a repeated broadcast: %+v, want sdk code 19 or 32 and hash %s", again, hash)
	}

	delegation := queryOK(t, g, staking, "Delegation", map[string]any{"delegator_addr": k.Address, "validator_addr": valoper})
	if resp, _ := delegation["delegation_response"].(map[string]any); amountOf(t, resp, "balance").Cmp(stake) != 0 {
		t.Errorf("Delegation: %v, want %s norama staked", delegation, stake.String())
	}
	if list, _ := queryOK(t, g, staking, "DelegatorDelegations", map[string]any{"delegator_addr": k.Address})["delegation_responses"].([]any); len(list) != 1 {
		t.Errorf("DelegatorDelegations: %d entries, want 1", len(list))
	}
	queryOK(t, g, distribution, "DelegationTotalRewards", map[string]any{"delegator_address": k.Address})
	queryOK(t, g, distribution, "DelegationRewards", map[string]any{"delegator_address": k.Address, "validator_address": valoper})
	if got := amountOf(t, queryOK(t, g, bank, "Balance", map[string]any{"address": k.Address, "denom": chain.Denom}), "balance"); got.Cmp(funded.Sub(stake)) > 0 {
		t.Errorf("balance %s norama after staking %s of %s", got.String(), stake.String(), funded.String())
	}
	query(t, g, staking, "Delegation", map[string]any{"delegator_addr": k.Address, "validator_addr": valoper}).Expect(t, http.StatusOK)
	c.RequireInvariants(t, "a wallet delegation")
}

// simulateAnswer is the 200 body of POST /v1/chain/simulate.
type simulateAnswer struct {
	// The gas figures are decimal strings: a bare JSON number above 2^53 is a different number to a
	// JavaScript client (RootWallet read the SDK's unlimited-meter limit, 2^64-1, that way).
	GasWanted uint64 `json:"gas_wanted,string"`
	GasUsed   uint64 `json:"gas_used,string"`
	Fee       struct {
		Denom  string `json:"denom"`
		Amount string `json:"amount"`
	} `json:"fee"`
	BaseFee string `json:"base_fee"`
}

func simulate(t *testing.T, g *gw.Client, raw []byte) simulateAnswer {
	t.Helper()
	var out simulateAnswer
	if err := postTx(t, g, "simulate", raw).Expect(t, http.StatusOK).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestWalletRoutes_contractInfoOfANonContractIs404: a wallet refuses a send to a contract before
// signing, so an address that is not one must be a typed 404 (it was a 502), and a malformed
// address a 400.
func TestWalletRoutes_contractInfoOfANonContractIs404(t *testing.T) {
	c := chain.New(t)
	g := gateway(t, c)
	n := c.FaucetNode(t)
	fresh := c.NewKey(t, n, "e2e-wallet-routes-eoa")
	funded := c.NewFundedKey(t, n, "e2e-wallet-routes-eoa-funded", chain.Orama(1))
	for name, address := range map[string]string{
		"an address that never existed":   fresh.Address,
		"an account that holds a balance": funded.Address,
	} {
		resp := query(t, g, wasm, "ContractInfo", map[string]any{"address": address})
		if resp.Status != http.StatusNotFound || strings.TrimSpace(string(resp.Body)) != "not found on chain" {
			t.Errorf("%s: HTTP %d %q, want 404 not found on chain", name, resp.Status, resp.Body)
		}
	}
	bad := query(t, g, wasm, "ContractInfo", map[string]any{"address": "orama1notanaddress"})
	if bad.Status != http.StatusBadRequest || leaks.MatchString(string(bad.Body)) {
		t.Errorf("a malformed address: HTTP %d %q, want a 400 without the node's message", bad.Status, bad.Body)
	}
	if got := query(t, g, auth, "Account", map[string]any{"address": fresh.Address}); got.Status != http.StatusNotFound {
		t.Errorf("auth Account of an address the chain never saw: HTTP %d, want 404", got.Status)
	}
}

// TestWalletRoutes_aRefusedTransactionIsATypedRefusal: the chain's refusal of a signed transaction
// (a user-to-user send, which x/bank refuses) and of undecodable bytes is a 422 with code,
// codespace and a log that names no path, source file or address.
func TestWalletRoutes_aRefusedTransactionIsATypedRefusal(t *testing.T) {
	c := chain.New(t)
	n := c.FaucetNode(t)
	g := gateway(t, c)
	k := c.NewFundedKey(t, n, "e2e-wallet-routes-refused", chain.Orama(2))
	other := c.NewKey(t, n, "e2e-wallet-routes-payee")
	raw := c.EncodeTx(t, n, c.Sign(t, k, chain.TxOptions{}, sendMsg(k.Address, other.Address, chain.Orama(1))))

	sim := decodeRefusal(t, postTx(t, g, "simulate", raw))
	if sim.Codespace == "" || sim.Log == "" {
		t.Errorf("a refused simulate: %+v, want a codespace and a log", sim)
	}
	if sim.TxHash != "" {
		t.Errorf("a refused simulate names a hash: %+v", sim)
	}
	garbage := decodeRefusal(t, postTx(t, g, "broadcast", []byte("not a transaction")))
	if garbage.Codespace != "sdk" || garbage.Log == "" {
		t.Errorf("undecodable bytes: %+v, want an sdk refusal with a reason", garbage)
	}
	if got := c.Bank(t, n, other.Address); !got.IsZero() {
		t.Errorf("a refused send paid %s norama", got.String())
	}
}

// TestWalletRoutes_badRequestsNeverReachTheChain: the route's own refusals, with their statuses.
func TestWalletRoutes_badRequestsNeverReachTheChain(t *testing.T) {
	c := chain.New(t)
	g := gateway(t, c)
	for _, route := range []string{"simulate", "broadcast"} {
		for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
			resp := g.MustSend(t, gw.Req{Method: method, Path: "/v1/chain/" + route})
			if resp.Status != http.StatusMethodNotAllowed || resp.Header.Get("Allow") != http.MethodPost {
				t.Errorf("%s %s: HTTP %d Allow %q, want 405 POST", method, route, resp.Status, resp.Header.Get("Allow"))
			}
		}
	}
	// Each of these POSTs draws a token of the route's bucket, so they go to simulate (burst 10).
	for name, tc := range map[string]struct {
		contentType, body string
		want              int
	}{
		"no content type":         {"", `{"tx_bytes":"YWJj"}`, http.StatusUnsupportedMediaType},
		"not json":                {"application/json", `tx`, http.StatusBadRequest},
		"missing tx_bytes":        {"application/json", `{}`, http.StatusBadRequest},
		"commit mode":             {"application/json", `{"tx_bytes":"YWJj","mode":"commit"}`, http.StatusBadRequest},
		"not base64":              {"application/json", `{"tx_bytes":"***"}`, http.StatusBadRequest},
		"a transaction too large": {"application/json", `{"tx_bytes":"` + base64Of(maxTxBytes+1) + `"}`, http.StatusRequestEntityTooLarge},
		"a body far too large":    {"application/json", `{"tx_bytes":"` + strings.Repeat("A", 3*maxTxBytes) + `"}`, http.StatusRequestEntityTooLarge},
	} {
		if resp := postBody(t, g, "simulate", tc.contentType, []byte(tc.body)); resp.Status != tc.want {
			t.Errorf("%s: HTTP %d, want %d: %.120s", name, resp.Status, tc.want, resp.Body)
		}
	}
	for name, tc := range map[string]struct {
		service, method string
		request         any
	}{
		"a page over the cap":       {bank, "AllBalances", map[string]any{"address": "orama1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqmg3rhc", "pagination": map[string]any{"limit": maxPageLimit + 1}}},
		"a method off the list":     {bank, "TotalSupply", nil},
		"a validator walk":          {staking, "Validators", nil},
		"a contract state walk":     {wasm, "AllContractState", map[string]any{"address": "orama1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqmg3rhc"}},
		"a Msg service by its name": {"cosmos.bank.v1beta1.Msg", "Send", nil},
	} {
		resp := query(t, g, tc.service, tc.method, tc.request)
		if want := map[bool]int{true: http.StatusBadRequest, false: http.StatusNotFound}[strings.HasPrefix(name, "a page")]; resp.Status != want {
			t.Errorf("%s: HTTP %d, want %d: %.120s", name, resp.Status, want, resp.Body)
		}
	}
}

// base64Of is the base64 text of n zero bytes.
func base64Of(n int) string { return base64.StdEncoding.EncodeToString(make([]byte, n)) }

// TestRoutes_rateLimitsAreTheirOwn: past its burst a client network is answered 429 with
// Retry-After on the transaction routes, in the retryable envelope, while the module-query route
// and a read of the explorer are still served: each has a bucket of its own. It runs last because
// it empties the buckets the other tests of the package draw on.
func TestRoutes_rateLimitsAreTheirOwn(t *testing.T) {
	c := chain.New(t)
	g := gateway(t, c)
	refused := 0
	var last *gw.Response
	for i := 0; i < 30 && refused == 0; i++ {
		resp := postBody(t, g, "broadcast", "application/json", []byte(`{}`))
		if resp.Status == http.StatusTooManyRequests {
			refused++
			last = resp
		} else if resp.Status != http.StatusBadRequest {
			t.Fatalf("broadcast %d of an empty request: HTTP %d, want 400 until the bucket is empty: %s", i, resp.Status, resp.Body)
		}
	}
	if last == nil {
		t.Fatal("30 broadcasts in a burst were never limited")
	}
	var envelope struct {
		Error struct {
			Code      string `json:"code"`
			Retryable bool   `json:"retryable"`
		} `json:"error"`
	}
	if err := last.Decode(&envelope); err != nil || envelope.Error.Code != "RATE_LIMITED" || !envelope.Error.Retryable || last.Header.Get("Retry-After") == "" {
		t.Errorf("the 429 is %s with Retry-After %q (%v), want the retryable RATE_LIMITED envelope", last.Body, last.Header.Get("Retry-After"), err)
	}
	query(t, g, staking, "Params", nil).Expect(t, http.StatusOK)
	g.MustSend(t, gw.Req{Path: "/v1/chain/status"}).Expect(t, http.StatusOK)
}
