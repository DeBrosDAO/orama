package gen

import (
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/ops"
	tokentypes "github.com/DeBrosOfficial/network/chain/x/token/types"
)

// TokenTransfer is the type name of a transfer of a non-norama token through x/token.
const TokenTransfer = "token-transfer"

const (
	// tokenSubdenomPrefix starts the subdenom of the token a run creates; the suffix makes it unique.
	tokenSubdenomPrefix = "bench"
	// tokenHolding is what every account is minted, so transfers of one unit never run dry.
	tokenHolding = 1_000_000_000
	tokenAmount  = 1
)

func init() { register(TokenTransfer, func() Generator { return &tokenTransfer{} }) }

// tokenTransfer sends one unit of a token created for the run from each account to its neighbour
// with x/token's MsgTransfer (the path that applies the token's powers, unlike a bank send).
type tokenTransfer struct {
	denom    string
	accounts []string
}

func (t *tokenTransfer) Name() string { return TokenTransfer }

func (t *tokenTransfer) Describe() string {
	return "x/token MsgTransfer of 1 unit of a token created for the run (a non-norama token)"
}

func (t *tokenTransfer) Prepare(ctx context.Context, env *Env, _ int) error {
	if len(env.Accounts) < 2 {
		return errors.New("token-transfer needs at least two accounts")
	}
	creator := env.Accounts[0]
	sub := tokenSubdenomPrefix + env.RunID
	create := &tokentypes.MsgCreateToken{Creator: creator.Address, Subdenom: sub, Name: "Bench token", Symbol: "BNCH", Mint: true}
	if _, err := env.Chain.Client.Submit(ctx, creator, create); err != nil {
		return fmt.Errorf("create the bench token: %w", err)
	}
	t.denom = tokentypes.Denom(creator.Address, sub)
	t.accounts = make([]string, len(env.Accounts))
	msgs := make([]sdk.Msg, len(env.Accounts))
	for i, a := range env.Accounts {
		t.accounts[i] = a.Address
		msgs[i] = &tokentypes.MsgMint{Sender: creator.Address, Denom: t.denom, Recipient: a.Address, Amount: math.NewInt(tokenHolding)}
	}
	env.Log("minting %s to %d accounts", t.denom, len(msgs))
	if err := env.Chain.Batch(ctx, creator, 0, ops.DefaultBatchTimeout, msgs...); err != nil {
		return fmt.Errorf("mint the bench token: %w", err)
	}
	return t.checkMinted(ctx, env)
}

// checkMinted reads back one holder: a sequence that advanced does not prove the mints executed.
func (t *tokenTransfer) checkMinted(ctx context.Context, env *Env) error {
	last := t.accounts[len(t.accounts)-1]
	got, err := env.Chain.Balance(ctx, last, t.denom)
	if err != nil {
		return err
	}
	if !got.Equal(math.NewInt(tokenHolding)) {
		return fmt.Errorf("account %s holds %s of %s after the mint, want %d: the mint did not execute", last, got, t.denom, tokenHolding)
	}
	return nil
}

func (t *tokenTransfer) Available() int { return Unlimited }

func (t *tokenTransfer) Plan(k int) (Planned, error) {
	from, to := pair(k, len(t.accounts))
	msg := &tokentypes.MsgTransfer{
		Sender: t.accounts[from], From: t.accounts[from], To: t.accounts[to],
		Denom: t.denom, Amount: math.NewInt(tokenAmount),
	}
	return Planned{Account: from, Msg: msg}, nil
}
