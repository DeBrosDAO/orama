package setup

import (
	"math/big"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/clusterreg"
)

func TestComputeBudget_storageBondBacksTheCapacity(t *testing.T) {
	p := planFor(t, Options{IPs: []string{ip1}, Name: "alice", StorageGB: 100})
	b, err := ComputeBudget(p, defaultParams())
	if err != nil {
		t.Fatal(err)
	}
	// 100 GB is 93.13 GiB, so 94 GiB at 1 ORAMA/GiB.
	if got := b.Bonds["alice"][clusterreg.RoleStorage].String(); got != "94000000000" {
		t.Errorf("storage bond %s, want 94 ORAMA", got)
	}
	// 94 + 1000 self-bond + 2 per node + 2 operator reserve + 2 for the node's hot key.
	if got := Orama(b.Total); got != "1100" {
		t.Errorf("total %s ORAMA, want 1100", got)
	}
	if got := Orama(b.HotKeys); got != "2" {
		t.Errorf("hot key funding %s ORAMA, want 2 for the one node", got)
	}
}

func TestComputeBudget_smallStorageFallsBackToTheRoleFloor(t *testing.T) {
	params := defaultParams()
	params.MinBond[clusterreg.RoleStorage] = big.NewInt(50 * noramaPerOrama)
	p := planFor(t, Options{IPs: []string{ip1}, Name: "alice", StorageGB: 1})
	b, err := ComputeBudget(p, params)
	if err != nil {
		t.Fatal(err)
	}
	if got := Orama(b.Bonds["alice"][clusterreg.RoleStorage]); got != "50" {
		t.Errorf("bond %s ORAMA, want the 50 floor", got)
	}
}

func TestComputeBudget_clusterOnlyCostsNothing(t *testing.T) {
	p := planFor(t, Options{IPs: []string{ip1}, ClusterOnly: true})
	b, err := ComputeBudget(p, defaultParams())
	if err != nil {
		t.Fatal(err)
	}
	if b.SelfBond.Sign() != 0 || len(b.Bonds) != 0 {
		t.Errorf("%+v", b)
	}
}

func TestComputeBudget_refusals(t *testing.T) {
	p := planFor(t, Options{IPs: []string{ip1}, Name: "alice"})
	noFloor := defaultParams()
	delete(noFloor.MinBond, clusterreg.RoleStorage)
	if _, err := ComputeBudget(p, noFloor); err == nil || !strings.Contains(err.Error(), "minimum bond") {
		t.Errorf("a missing floor: %v", err)
	}
	noPerGiB := defaultParams()
	noPerGiB.BondPerGiB = nil
	if _, err := ComputeBudget(p, noPerGiB); err == nil || !strings.Contains(err.Error(), "per GiB") {
		t.Errorf("a missing bond per GiB: %v", err)
	}
}

func TestOrama(t *testing.T) {
	for in, want := range map[string]string{"0": "0", "1000000000": "1", "1500000000": "1.5", "1234567890": "1.234", "999": "0", "1000000": "0.001", "1014000000000": "1014"} {
		n, _ := new(big.Int).SetString(in, 10)
		if got := Orama(n); got != want {
			t.Errorf("Orama(%s) = %q, want %q", in, got, want)
		}
	}
}

// Every full node holds a name, and the chain locks a deposit for it.
func TestComputeBudget_eachFullNodeSetsAsideItsNameDeposit(t *testing.T) {
	params := defaultParams()
	params.NameDeposit = big.NewInt(noramaPerOrama)
	p := planFor(t, Options{IPs: []string{ip1, ip2}, Name: "alice", StorageGB: 10})
	with, err := ComputeBudget(p, params)
	if err != nil {
		t.Fatal(err)
	}
	without, err := ComputeBudget(p, defaultParams())
	if err != nil {
		t.Fatal(err)
	}
	if got := new(big.Int).Sub(with.Total, without.Total); got.Cmp(big.NewInt(2*noramaPerOrama)) != 0 {
		t.Errorf("two nodes added %s norama, want two deposits of 1 ORAMA", got)
	}
	if with.NameDeposit.Cmp(big.NewInt(noramaPerOrama)) != 0 {
		t.Errorf("per-node deposit %s", with.NameDeposit)
	}
}

func TestComputeBudget_clusterOnlyLocksNoNameDeposit(t *testing.T) {
	params := defaultParams()
	params.NameDeposit = big.NewInt(noramaPerOrama)
	b, err := ComputeBudget(planFor(t, Options{IPs: []string{ip1}, ClusterOnly: true}), params)
	if err != nil {
		t.Fatal(err)
	}
	if b.Total.Sign() != 0 && b.Reserve.Cmp(big.NewInt(feeReserveOperator)) != 0 {
		t.Errorf("reserve %s", b.Reserve)
	}
}
