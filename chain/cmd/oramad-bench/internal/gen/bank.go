package gen

import (
	"context"
	"errors"

	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
)

// BankSend is the type name of a public transfer of norama between two accounts.
const BankSend = "bank-send"

// bankAmount is what each bank send moves, in norama.
const bankAmount = 1

func init() { register(BankSend, func() Generator { return &bankSend{} }) }

// bankSend sends one norama from each account to its neighbour with x/bank's MsgSend.
type bankSend struct{ accounts []string }

func (b *bankSend) Name() string { return BankSend }

func (b *bankSend) Describe() string {
	return "x/bank MsgSend of 1 norama between two bench accounts (a public transfer of the base token)"
}

func (b *bankSend) Prepare(_ context.Context, env *Env, _ int) error {
	if len(env.Accounts) < 2 {
		return errors.New("bank-send needs at least two accounts")
	}
	b.accounts = make([]string, len(env.Accounts))
	for i, a := range env.Accounts {
		b.accounts[i] = a.Address
	}
	return nil
}

func (b *bankSend) Available() int { return Unlimited }

func (b *bankSend) Plan(k int) (Planned, error) {
	from, to := pair(k, len(b.accounts))
	msg := banktypes.NewMsgSend(
		sdk.MustAccAddressFromBech32(b.accounts[from]), sdk.MustAccAddressFromBech32(b.accounts[to]),
		sdk.NewCoins(sdk.NewInt64Coin(params.BaseDenom, bankAmount)),
	)
	return Planned{Account: from, Msg: msg}, nil
}
