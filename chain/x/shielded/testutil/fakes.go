package testutil

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	nodestypes "github.com/DeBrosOfficial/network/chain/x/nodes/types"
	"github.com/DeBrosOfficial/network/chain/x/shielded/bundle"
	"github.com/DeBrosOfficial/network/chain/x/shielded/verify"
)

// Verifier is a test verifier. It accepts every bundle unless Reject says otherwise.
type Verifier struct {
	// Name is the verifier's ID; verify.Check needs two distinct ones.
	Name     string
	Reject   func(bundle []byte) error
	Calls    int
	Bindings [][]byte
}

// ID implements verify.Verifier.
func (v *Verifier) ID() string { return v.Name }

// Verify implements verify.Verifier.
func (v *Verifier) Verify(b, binding []byte) error {
	v.Calls++
	v.Bindings = append(v.Bindings, append([]byte(nil), binding...))
	if v.Reject != nil {
		return v.Reject(b)
	}
	return nil
}

var _ verify.Verifier = (*Verifier)(nil)

// Tree is a stand-in for the Orchard tree: its frontier is every commitment appended so far and
// its root the SHA-256 of them. It has the interface of the real tree and none of its hashing.
type Tree struct{}

var emptyRoot = sha256.Sum256([]byte("test-empty-tree"))

// EmptyRoot is the root of the empty test tree.
func (Tree) EmptyRoot() ([bundle.NodeLen]byte, error) { return emptyRoot, nil }

// Append adds the commitments.
func (Tree) Append(frontier []byte, cmx [][bundle.NodeLen]byte) ([]byte, [bundle.NodeLen]byte, error) {
	next := append([]byte(nil), frontier...)
	for _, c := range cmx {
		next = append(next, c[:]...)
	}
	if len(next) == 0 {
		return nil, emptyRoot, nil
	}
	return next, sha256.Sum256(next), nil
}

// RootAfter is the root of a test tree holding these commitments in order.
func RootAfter(cmx ...[bundle.NodeLen]byte) [bundle.NodeLen]byte {
	_, root, _ := Tree{}.Append(nil, cmx)
	return root
}

// Bank is an in-memory bank keeper keyed by address string, module accounts by module name.
type Bank struct {
	Balances map[string]math.Int
	Burned   math.Int
}

// NewBank returns an empty bank.
func NewBank() *Bank { return &Bank{Balances: map[string]math.Int{}, Burned: math.ZeroInt()} }

// Of is the balance of an account key: an address string or a module name.
func (b *Bank) Of(key string) math.Int {
	if v, ok := b.Balances[key]; ok {
		return v
	}
	return math.ZeroInt()
}

// Fund adds to an account.
func (b *Bank) Fund(key string, amt int64) { b.Balances[key] = b.Of(key).AddRaw(amt) }

func (b *Bank) move(from, to string, amt sdk.Coins) error {
	if len(amt) != 1 || amt[0].Denom != params.BaseDenom || !amt[0].Amount.IsPositive() {
		return fmt.Errorf("bad coins %s", amt)
	}
	if b.Of(from).LT(amt[0].Amount) {
		return fmt.Errorf("insufficient funds in %s: have %s, need %s", from, b.Of(from), amt[0].Amount)
	}
	b.Balances[from] = b.Of(from).Sub(amt[0].Amount)
	if to != "" {
		b.Balances[to] = b.Of(to).Add(amt[0].Amount)
	} else {
		b.Burned = b.Burned.Add(amt[0].Amount)
	}
	return nil
}

// SendCoinsFromAccountToModule implements the shielded BankKeeper.
func (b *Bank) SendCoinsFromAccountToModule(_ context.Context, from sdk.AccAddress, module string, amt sdk.Coins) error {
	return b.move(from.String(), module, amt)
}

// SendCoinsFromModuleToAccount implements the shielded BankKeeper.
func (b *Bank) SendCoinsFromModuleToAccount(_ context.Context, module string, to sdk.AccAddress, amt sdk.Coins) error {
	return b.move(module, to.String(), amt)
}

// SendCoinsFromModuleToModule implements the shielded BankKeeper.
func (b *Bank) SendCoinsFromModuleToModule(_ context.Context, from, to string, amt sdk.Coins) error {
	return b.move(from, to, amt)
}

// BurnCoins implements the shielded BankKeeper.
func (b *Bank) BurnCoins(_ context.Context, module string, amt sdk.Coins) error {
	return b.move(module, "", amt)
}

// GetBalance implements the shielded BankKeeper; a module address reads the module's balance.
func (b *Bank) GetBalance(_ context.Context, addr sdk.AccAddress, denom string) sdk.Coin {
	for key := range b.Balances {
		if authtypes.NewModuleAddress(key).Equals(addr) {
			return sdk.NewCoin(denom, b.Of(key))
		}
	}
	return sdk.NewCoin(denom, b.Of(addr.String()))
}

// Fees is an in-memory x/fees: earnings ledger, base fee and proposer.
type Fees struct {
	Bank         *Bank
	Earnings     map[string]math.Int
	BaseFee      math.Int
	Proposer     sdk.AccAddress
	EarningsAcct string
}

// NewFees returns fees with a 1 norama/gas base fee and no proposer.
func NewFees(bank *Bank) *Fees {
	return &Fees{Bank: bank, Earnings: map[string]math.Int{}, BaseFee: math.OneInt(), EarningsAcct: "fees"}
}

// EarningsOf is an address's earnings.
func (f *Fees) EarningsOf(addr sdk.AccAddress) math.Int {
	if v, ok := f.Earnings[addr.String()]; ok {
		return v
	}
	return math.ZeroInt()
}

// GrantEarnings gives an address earnings, with the coins in the earnings account.
func (f *Fees) GrantEarnings(addr sdk.AccAddress, amt int64) {
	f.Earnings[addr.String()] = f.EarningsOf(addr).AddRaw(amt)
	f.Bank.Fund(f.EarningsAcct, amt)
}

// CreditEarnings implements the shielded FeesKeeper.
func (f *Fees) CreditEarnings(ctx context.Context, module string, addr sdk.AccAddress, amt sdk.Coin) error {
	if err := f.Bank.SendCoinsFromModuleToModule(ctx, module, f.EarningsAcct, sdk.NewCoins(amt)); err != nil {
		return err
	}
	f.Earnings[addr.String()] = f.EarningsOf(addr).Add(amt.Amount)
	return nil
}

// DebitEarningsUpTo implements the shielded FeesKeeper.
func (f *Fees) DebitEarningsUpTo(_ context.Context, addr sdk.AccAddress, want math.Int) (math.Int, error) {
	debit := math.MinInt(want, f.EarningsOf(addr))
	f.Earnings[addr.String()] = f.EarningsOf(addr).Sub(debit)
	return debit, nil
}

// BaseFeePerGas implements the shielded FeesKeeper.
func (f *Fees) BaseFeePerGas(context.Context) (math.Int, error) { return f.BaseFee, nil }

// EarningsModule implements the shielded FeesKeeper.
func (f *Fees) EarningsModule() string { return f.EarningsAcct }

// ProposerAccount implements the shielded FeesKeeper.
func (f *Fees) ProposerAccount(sdk.Context) (sdk.AccAddress, bool) {
	return f.Proposer, len(f.Proposer) > 0
}

// Bonder records delegations and moves the coins out of the owner's account.
type Bonder struct {
	Bank      *Bank
	Delegated map[string]math.Int
	Fail      error
	// Minimum is the smallest delegation CheckMinimum accepts.
	Minimum math.Int
}

// NewBonder returns an empty bonder.
func NewBonder(bank *Bank) *Bonder {
	return &Bonder{Bank: bank, Delegated: map[string]math.Int{}, Minimum: math.ZeroInt()}
}

// CheckMinimum implements the shielded Bonder.
func (b *Bonder) CheckMinimum(_ sdk.Context, _ sdk.AccAddress, _ string, amount math.Int) error {
	if amount.LT(b.Minimum) {
		return errors.New("delegation below the minimum")
	}
	return nil
}

// Delegate implements the shielded Bonder.
func (b *Bonder) Delegate(ctx sdk.Context, delegator sdk.AccAddress, validator string, amount math.Int) error {
	if b.Fail != nil {
		return b.Fail
	}
	if err := b.Bank.move(delegator.String(), "bonded", sdk.NewCoins(sdk.NewCoin(params.BaseDenom, amount))); err != nil {
		return err
	}
	key := delegator.String() + "/" + validator
	if cur, ok := b.Delegated[key]; ok {
		amount = amount.Add(cur)
	}
	b.Delegated[key] = amount
	return nil
}

// NodeBonder records node bonds.
type NodeBonder struct {
	Bank  *Bank
	Bonds []*nodestypes.MsgBondNode
	Fail  error
}

// BondNode implements the shielded NodeBonder.
func (n *NodeBonder) BondNode(_ sdk.Context, msg *nodestypes.MsgBondNode) error {
	if n.Fail != nil {
		return n.Fail
	}
	owner, err := sdk.AccAddressFromBech32(msg.Operator)
	if err != nil {
		return err
	}
	if err := n.Bank.move(owner.String(), "nodes", sdk.NewCoins(sdk.NewCoin(params.BaseDenom, msg.Amount))); err != nil {
		return err
	}
	n.Bonds = append(n.Bonds, msg)
	return nil
}

// ErrBoom is a sentinel a test injects as a failure.
var ErrBoom = errors.New("boom")

func paramsInt(v int64) math.Int { return math.NewInt(v) }

// snapshot is a copy of the fakes' state. The real bank and fee keepers live in the multistore and
// roll back with it; the fakes are maps, so the Env copies them at the same points.
type snapshot struct {
	balances  map[string]math.Int
	burned    math.Int
	earnings  map[string]math.Int
	delegated map[string]math.Int
	bonds     int
}

func cloneInts(m map[string]math.Int) map[string]math.Int {
	out := make(map[string]math.Int, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
