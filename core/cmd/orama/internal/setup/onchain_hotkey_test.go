package setup

import (
	"context"
	"errors"
	"math/big"
	"net/http"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/clusterreg"
)

// hotKeyOf is the hot key address the fake machine at ip reports.
func hotKeyOf(ip string) string { return "orama1hot" + ip }

func TestRun_fundsTheHotKeyOfANewNodeFromTheBankAfterItsProviderStarts(t *testing.T) {
	h := newHarness()
	mustRun(t, h, h.opts(ip1))

	want := "tx fund-hot-key alice amount=2000000000 bank=true"
	if h.w.count(want) != 1 {
		t.Fatalf("want one %q:\n%s", want, strings.Join(h.w.entries(), "\n"))
	}
	if h.w.index("tx register-node alice") > h.w.index(want) || h.w.index("start "+ip1+" alice") > h.w.index(want) {
		t.Errorf("the hot key is funded once the node is registered and its provider started:\n%s", strings.Join(h.w.entries(), "\n"))
	}
	if got := h.w.feeBalances[hotKeyOf(ip1)]; got == nil || got.Cmp(big.NewInt(hotKeyFundingPerNode)) != 0 {
		t.Errorf("fee balance %v, want %d", got, hotKeyFundingPerNode)
	}
}

func TestRun_fundsEveryFullNodesHotKey(t *testing.T) {
	h := newHarness()
	mustRun(t, h, h.opts(ip1, ip2, ip3))
	if got := h.w.count("tx fund-hot-key "); got != 3 {
		t.Errorf("%d hot keys funded, want one per node:\n%s", got, strings.Join(h.w.entries(), "\n"))
	}
}

func TestRun_aHotKeyThatAlreadyHoldsTheFundingIsNotFundedAgain(t *testing.T) {
	h := newHarness()
	h.w.operatorRegistered = true
	h.w.nodes["alice"] = &RegisteredNode{Roles: []int{clusterreg.RoleStorage}, Bonds: map[int]*big.Int{}, HotKey: hotKeyOf(ip1)}
	h.w.feeBalances[hotKeyOf(ip1)] = big.NewInt(hotKeyFundingPerNode + 1)
	mustRun(t, h, h.opts(ip1))
	if h.w.index("tx fund-hot-key") >= 0 {
		t.Errorf("a covered hot key was funded:\n%s", strings.Join(h.w.entries(), "\n"))
	}
	if !h.report.has(ip1, StepOnchain, StateSkipped) {
		t.Error("the skipped funding is reported")
	}
}

func TestRun_aHotKeyThatHoldsPartOfTheFundingIsGivenTheDifference(t *testing.T) {
	h := newHarness()
	h.w.operatorRegistered = true
	h.w.nodes["alice"] = &RegisteredNode{Roles: []int{clusterreg.RoleStorage}, Bonds: map[int]*big.Int{}, HotKey: hotKeyOf(ip1)}
	h.w.feeBalances[hotKeyOf(ip1)] = big.NewInt(hotKeyFundingPerNode - 300)
	mustRun(t, h, h.opts(ip1))
	if h.w.count("tx fund-hot-key alice amount=300 bank=true") != 1 {
		t.Errorf("only the missing 300 norama are sent:\n%s", strings.Join(h.w.entries(), "\n"))
	}
}

func TestRun_aFailedHotKeyFundingNamesTheNodeAndTheHotKey(t *testing.T) {
	h := newHarness()
	h.w.fundHotKeyErr = errors.New("insufficient bank balance")
	_, err := run(t, h, h.opts(ip1))
	if err == nil {
		t.Fatal("a failed funding was swallowed")
	}
	for _, want := range []string{ip1, `node "alice"`, hotKeyOf(ip1), "insufficient bank balance"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
	if !h.report.has(ip1, StepOnchain, StateFailed) {
		t.Error("the node's on-chain step is reported failed")
	}
	if h.w.index("claim alice") >= 0 {
		t.Error("nothing follows a node whose hot key could not be funded")
	}
}

func TestRun_aFundedHotKeyIsNotAskedForAgainInTheBudget(t *testing.T) {
	// The node is registered and bonded, its name held, its validator created: what is left to pay for
	// is the operator's own 2 ORAMA reserve and the hot key. A funded hot key takes 2 ORAMA off the need.
	for name, tc := range map[string]struct {
		funded  int64
		balance int64
		wantErr bool
	}{
		"unfunded hot key and 2 ORAMA":   {0, 2 * noramaPerOrama, true},
		"unfunded hot key and 4 ORAMA":   {0, 4 * noramaPerOrama, false},
		"funded hot key and 2 ORAMA":     {hotKeyFundingPerNode, 2 * noramaPerOrama, false},
		"funded hot key and less than 2": {hotKeyFundingPerNode, noramaPerOrama, true},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness()
			h.w.balance = big.NewInt(tc.balance)
			h.w.operatorRegistered, h.w.validator = true, true
			h.w.names["alice"] = "alice"
			h.w.nodes["alice"] = &RegisteredNode{
				Roles: []int{clusterreg.RoleStorage}, CapacityBytes: 10_000_000_000, HotKey: hotKeyOf(ip1),
				Bonds: map[int]*big.Int{clusterreg.RoleStorage: big.NewInt(10 * noramaPerOrama)},
			}
			h.w.feeBalances[hotKeyOf(ip1)] = big.NewInt(tc.funded)
			_, err := run(t, h, h.opts(ip1))
			var nf *NotFundedError
			if tc.wantErr != errors.As(err, &nf) {
				t.Fatalf("error %v, want a NotFundedError: %v", err, tc.wantErr)
			}
		})
	}
}

func TestComputeBudget_includesTheHotKeyOfEveryFullNode(t *testing.T) {
	one := planFor(t, Options{IPs: []string{ip1}, Name: "alice"})
	three := planFor(t, Options{IPs: []string{ip1, ip2, ip3}, Name: "alice"})
	b1, err := ComputeBudget(one, defaultParams())
	if err != nil {
		t.Fatal(err)
	}
	b3, err := ComputeBudget(three, defaultParams())
	if err != nil {
		t.Fatal(err)
	}
	if b1.HotKeys.Cmp(big.NewInt(hotKeyFundingPerNode)) != 0 || b3.HotKeys.Cmp(big.NewInt(3*hotKeyFundingPerNode)) != 0 {
		t.Errorf("hot key funding %s and %s", b1.HotKeys, b3.HotKeys)
	}
	parts := new(big.Int).Add(bondTotal(b1), b1.SelfBond)
	parts.Add(parts, b1.Reserve).Add(parts, b1.HotKeys)
	if parts.Cmp(b1.Total) != 0 {
		t.Errorf("total %s is not bonds, self-bond, reserve and hot keys (%s)", b1.Total, parts)
	}
	if clusterOnly, err := ComputeBudget(planFor(t, Options{IPs: []string{ip1}, ClusterOnly: true}), defaultParams()); err != nil || clusterOnly.HotKeys.Sign() != 0 {
		t.Errorf("a cluster-only run funds no hot key: %v, %v", clusterOnly, err)
	}
}

func TestRestSession_feeBalanceIsReadOrZero(t *testing.T) {
	path := "/orama/fees/v1/fee-balance/" + hotKeyOf(ip1)
	s := chainServer(t, map[string]func(http.ResponseWriter){path: jsonBody(`{"balance":"1500000000"}`)})
	got, err := s.FeeBalance(context.Background(), hotKeyOf(ip1))
	if err != nil || got.String() != "1500000000" {
		t.Fatalf("%v, %v", got, err)
	}
	for name, srv := range map[string]*restSession{
		"a 404":        chainServer(t, nil),
		"an empty one": chainServer(t, map[string]func(http.ResponseWriter){path: jsonBody(`{}`)}),
	} {
		if zero, err := srv.FeeBalance(context.Background(), hotKeyOf(ip1)); err != nil || zero.Sign() != 0 {
			t.Errorf("%s is a fee balance of nothing: %v, %v", name, zero, err)
		}
	}
	bad := chainServer(t, map[string]func(http.ResponseWriter){path: jsonBody(`{"balance":"-5"}`)})
	if _, err := bad.FeeBalance(context.Background(), hotKeyOf(ip1)); err == nil {
		t.Error("a fee balance that is not an amount is an error")
	}
	down := chainServer(t, map[string]func(http.ResponseWriter){path: func(w http.ResponseWriter) { http.Error(w, "locked", http.StatusInternalServerError) }})
	if _, err := down.FeeBalance(context.Background(), hotKeyOf(ip1)); err == nil {
		t.Error("a server error is not a balance of zero")
	}
}

func TestRestSession_aNodeIsReadWithItsHotKey(t *testing.T) {
	s := chainServer(t, map[string]func(http.ResponseWriter){
		"/orama/nodes/v1/node/alice": jsonBody(`{"node":{"hot_key":"` + hotKeyOf(ip1) + `"}}`),
	})
	n, err := s.Node(context.Background(), "alice")
	if err != nil || n == nil || n.HotKey != hotKeyOf(ip1) {
		t.Fatalf("%+v, %v", n, err)
	}
}
