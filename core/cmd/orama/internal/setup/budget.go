package setup

import (
	"fmt"
	"math/big"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/clusterreg"
	"github.com/DeBrosOfficial/network/pkg/onchain"
)

const (
	// norama per ORAMA, for messages.
	noramaPerOrama = 1_000_000_000
	// bytesPerGB and bytesPerGiB: the operator counts storage in GB; the chain
	// prices capacity per GiB.
	bytesPerGB  = 1_000_000_000
	bytesPerGiB = 1 << 30
	// feeReservePerNode and feeReserveOperator cover the transaction fees and the
	// node record's state deposit: a node sends a registration, up to three
	// bonds and a capacity declaration, and the operator a registration and a
	// validator. The chain's base fee moves, so this is generous rather than exact.
	feeReservePerNode  = 2 * noramaPerOrama
	feeReserveOperator = 2 * noramaPerOrama
)

// Budget is what a run bonds and spends on the chain.
type Budget struct {
	// Bonds is the role bonds of each node, by node name.
	Bonds map[string]map[int]*big.Int
	// SelfBond is the validator's self-bond; zero when no validator is created.
	SelfBond *big.Int
	// Reserve is the fee and deposit allowance.
	Reserve *big.Int
	// Total is everything the operator account must hold.
	Total *big.Int
}

// StorageCapacityBytes is the capacity a node of storageGB declares.
func StorageCapacityBytes(storageGB uint64) uint64 { return storageGB * bytesPerGB }

// ComputeBudget prices the plan against the chain's parameters. A bond is the
// role's floor, except storage, whose bond backs the capacity the node declares:
// bond_per_gib for each GiB of it.
func ComputeBudget(p *Plan, params ChainParams) (*Budget, error) {
	b := &Budget{Bonds: map[string]map[int]*big.Int{}, SelfBond: new(big.Int), Reserve: new(big.Int).SetUint64(feeReserveOperator), Total: new(big.Int)}
	for _, n := range p.Nodes {
		if !n.Full() {
			continue
		}
		bonds, err := nodeBonds(n, params)
		if err != nil {
			return nil, err
		}
		b.Bonds[n.Name] = bonds
		b.Reserve.Add(b.Reserve, new(big.Int).SetUint64(feeReservePerNode))
		for _, amount := range bonds {
			b.Total.Add(b.Total, amount)
		}
		if n.Validator {
			self, ok := new(big.Int).SetString(onchain.DefaultSelfBondNorama, 10)
			if !ok {
				return nil, fmt.Errorf("the default self-bond %q is not a number", onchain.DefaultSelfBondNorama)
			}
			b.SelfBond.Set(self)
		}
	}
	b.Total.Add(b.Total, b.SelfBond)
	b.Total.Add(b.Total, b.Reserve)
	return b, nil
}

func nodeBonds(n NodePlan, params ChainParams) (map[int]*big.Int, error) {
	bonds := map[int]*big.Int{}
	for _, role := range n.Roles {
		floor, ok := params.MinBond[role]
		if !ok || floor.Sign() <= 0 {
			return nil, fmt.Errorf("the chain lists no minimum bond for role %d: is this the chain the manifest names?", role)
		}
		amount := new(big.Int).Set(floor)
		if role == clusterreg.RoleStorage {
			backing, err := storageBond(StorageCapacityBytes(n.StorageGB), params)
			if err != nil {
				return nil, err
			}
			if backing.Cmp(amount) > 0 {
				amount = backing
			}
		}
		bonds[role] = amount
	}
	return bonds, nil
}

// storageBond is the bond that backs capacityBytes: ceil(capacity / GiB) times
// bond_per_gib, which is what x/nodes caps a declaration by.
func storageBond(capacityBytes uint64, params ChainParams) (*big.Int, error) {
	if params.BondPerGiB == nil || params.BondPerGiB.Sign() <= 0 {
		return nil, fmt.Errorf("the chain lists no bond per GiB of storage")
	}
	gib := new(big.Int).SetUint64(bytesPerGiB)
	units := new(big.Int).Add(new(big.Int).SetUint64(capacityBytes), new(big.Int).Sub(gib, big.NewInt(1)))
	units.Quo(units, gib)
	return units.Mul(units, params.BondPerGiB), nil
}

// Orama renders norama as ORAMA with up to three decimals, trailing zeros dropped.
func Orama(norama *big.Int) string {
	whole, frac := new(big.Int).QuoRem(norama, big.NewInt(noramaPerOrama), new(big.Int))
	thousandths := frac.Int64() * 1000 / noramaPerOrama
	if thousandths == 0 {
		return whole.String()
	}
	return strings.TrimRight(fmt.Sprintf("%s.%03d", whole, thousandths), "0")
}

// NotFundedError says what the operator account is missing.
type NotFundedError struct {
	Address string
	Have    *big.Int
	Need    *big.Int
	Budget  *Budget
	// FaucetTried says the network's faucet was asked and could not cover it.
	FaucetTried string
}

func (e *NotFundedError) Error() string {
	short := new(big.Int).Sub(e.Need, e.Have)
	breakdown := ""
	if e.Budget != nil && e.Need.Cmp(e.Budget.Total) == 0 {
		breakdown = fmt.Sprintf(" (bonds %s, validator self-bond %s, fees and deposits %s)",
			Orama(bondTotal(e.Budget)), Orama(e.Budget.SelfBond), Orama(e.Budget.Reserve))
	}
	msg := fmt.Sprintf("the operator account %s holds %s ORAMA and this setup needs %s ORAMA%s: send at least %s ORAMA to %s, then run the same `orama setup` command again; it resumes where it stopped",
		e.Address, Orama(e.Have), Orama(e.Need), breakdown, Orama(short), e.Address)
	if e.FaucetTried != "" {
		msg += "\n  the network's faucet could not fund it: " + e.FaucetTried
	}
	return msg
}

func bondTotal(b *Budget) *big.Int {
	t := new(big.Int).Set(b.Total)
	t.Sub(t, b.SelfBond)
	return t.Sub(t, b.Reserve)
}
