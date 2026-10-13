package app_test

import (
	"fmt"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	emissiontypes "github.com/DeBrosOfficial/network/chain/x/emission/types"
	tokentypes "github.com/DeBrosOfficial/network/chain/x/token/types"
)

// Operation weights, out of 100: the cumulative upper bound of each kind.
const (
	simWeightSend     = 15
	simWeightFaucet   = 20
	simWeightStaking  = 28
	simWeightCreate   = 36
	simWeightMint     = 52
	simWeightTransfer = 76
	simWeightPlain    = 82
	simWeightBurn     = 90
	simWeightFreeze   = 95
)

func (c *simChain) randomTx(s int) simTx {
	roll := c.rng.Intn(100)
	switch {
	case roll < simWeightSend:
		return c.opSend(s)
	case roll < simWeightFaucet:
		return c.opFaucet(s)
	case roll < simWeightStaking:
		return c.opStaking(s)
	case roll < simWeightCreate || len(c.tokens) == 0:
		return c.opCreate(s)
	case roll < simWeightMint:
		return c.opMint(s)
	case roll < simWeightTransfer:
		return c.opTransfer(s)
	case roll < simWeightPlain:
		return c.opPlainSend(s)
	case roll < simWeightBurn:
		return c.opBurn(s)
	case roll < simWeightFreeze:
		return c.opFreeze(s)
	default:
		return c.opPause(s)
	}
}

func (c *simChain) other(s int) sdk.AccAddress {
	return c.users[(s+1+c.rng.Intn(simUserCount-1))%simUserCount].addr
}

// amount draws a positive amount up to max, and now and then zero, which every operation refuses.
func (c *simChain) amount(max int64) math.Int {
	if c.rng.Intn(12) == 0 {
		return math.ZeroInt()
	}
	if max < 1 {
		max = 1
	}
	return math.NewInt(1 + c.rng.Int63n(max))
}

func predictionOf(ok bool) simPrediction {
	if ok {
		return simMustSucceed
	}
	return simMustFail
}

func (c *simChain) opSend(s int) simTx {
	to := c.other(s)
	if c.rng.Intn(10) == 0 {
		to = authtypes.NewModuleAddress(emissiontypes.ModuleName) // a module account: the bank refuses it
	}
	amt := sdk.NewCoins(sdk.NewInt64Coin(params.BaseDenom, 1+c.rng.Int63n(2*params.NoramaPerOrama)))
	return simTx{signer: s, label: "bank send norama", msg: &banktypes.MsgSend{FromAddress: c.users[s].addr.String(), ToAddress: to.String(), Amount: amt}}
}

func (c *simChain) opFaucet(s int) simTx {
	drip := math.NewInt(1 + c.rng.Int63n(simMaxDrip+simMaxDrip/10)) // sometimes above the max drip
	return simTx{signer: s, label: "faucet drip", msg: &emissiontypes.MsgFaucet{Signer: c.users[s].addr.String(), Recipient: c.other(s).String(), Amount: drip}}
}

func (c *simChain) opStaking(s int) simTx {
	coin := sdk.NewInt64Coin(params.BaseDenom, (1+c.rng.Int63n(5))*params.NoramaPerOrama)
	if c.rng.Intn(3) == 0 {
		return simTx{signer: s, label: "undelegate", msg: &stakingtypes.MsgUndelegate{DelegatorAddress: c.users[s].addr.String(), ValidatorAddress: c.valoper.String(), Amount: coin}}
	}
	return simTx{signer: s, label: "delegate", msg: &stakingtypes.MsgDelegate{DelegatorAddress: c.users[s].addr.String(), ValidatorAddress: c.valoper.String(), Amount: coin}}
}

func (c *simChain) opCreate(s int) simTx {
	c.created++
	sub := fmt.Sprintf("tk%d", c.created)
	creator := c.users[s].addr
	denom := tokentypes.Denom(creator.String(), sub)
	mint, freeze, pause := c.rng.Intn(10) < 8, c.rng.Intn(2) == 0, c.rng.Intn(2) == 0
	var bps int64
	if c.rng.Intn(10) < 3 {
		bps = 100 + c.rng.Int63n(400)
	}
	nonTransferable := c.rng.Intn(10) == 0
	msg := &tokentypes.MsgCreateToken{
		Creator: creator.String(), Subdenom: sub, Name: "Sim token", Symbol: "SIM",
		Mint: mint, Freeze: freeze, Pause: pause, TransferFeeBps: uint32(bps), NonTransferable: nonTransferable,
	}
	return simTx{signer: s, label: "token create", msg: msg, onOK: func() {
		c.tokens[denom] = newSimToken(denom, creator, mint, freeze, pause, bps, nonTransferable)
	}}
}

// tokenFor picks a token for the user to act on, preferring one that satisfies want. It returns nil, most
// of the time, when the user has none, so that valid operations dominate the run.
func (c *simChain) tokenFor(s int, want func(*simToken) bool) *simToken {
	var preferred, any []*simToken
	for _, denom := range simDenoms(c.tokens) {
		t := c.tokens[denom]
		any = append(any, t)
		if want(t) {
			preferred = append(preferred, t)
		}
	}
	if len(preferred) > 0 && c.rng.Intn(10) < 8 {
		return preferred[c.rng.Intn(len(preferred))]
	}
	if len(preferred) == 0 && c.rng.Intn(10) < 8 {
		return nil // nothing the user can validly act on: the caller creates a token instead
	}
	return any[c.rng.Intn(len(any))]
}

func (c *simChain) opMint(s int) simTx {
	me := c.users[s].addr
	t := c.tokenFor(s, func(t *simToken) bool { return t.creator.Equals(me) && t.canMint })
	if t == nil {
		return c.opCreate(s)
	}
	to := c.users[c.rng.Intn(simUserCount)].addr
	amt := c.amount(1_000_000)
	ok := t.predictMint(me, to, amt)
	return simTx{signer: s, label: "token mint", predict: predictionOf(ok),
		msg: &tokentypes.MsgMint{Sender: me.String(), Denom: t.denom, Recipient: to.String(), Amount: amt}}
}

func (c *simChain) opTransfer(s int) simTx {
	me := c.users[s].addr
	t := c.tokenFor(s, func(t *simToken) bool { return t.balance(me).IsPositive() && t.canMove(me, c.other(s), math.OneInt()) })
	if t == nil {
		return c.opCreate(s)
	}
	to := c.other(s)
	amt := c.amount(t.balance(me).Int64() + 2)
	ok := t.predictTransfer(me, to, amt)
	return simTx{signer: s, label: "token transfer", predict: predictionOf(ok),
		msg: &tokentypes.MsgTransfer{Sender: me.String(), From: me.String(), To: to.String(), Denom: t.denom, Amount: amt}}
}

func (c *simChain) opPlainSend(s int) simTx {
	me := c.users[s].addr
	t := c.tokenFor(s, func(t *simToken) bool { return t.balance(me).IsPositive() && t.feeBps == 0 })
	if t == nil {
		return c.opCreate(s)
	}
	to := c.other(s)
	amt := c.amount(t.balance(me).Int64() + 2)
	ok := t.predictPlainSend(me, to, amt)
	return simTx{signer: s, label: "bank send token", predict: predictionOf(ok),
		msg: &banktypes.MsgSend{FromAddress: me.String(), ToAddress: to.String(), Amount: sdk.NewCoins(sdk.NewCoin(t.denom, amt))}}
}

func (c *simChain) opBurn(s int) simTx {
	me := c.users[s].addr
	t := c.tokenFor(s, func(t *simToken) bool { return t.balance(me).IsPositive() })
	if t == nil {
		return c.opCreate(s)
	}
	amt := c.amount(t.balance(me).Int64() + 2)
	ok := t.predictBurn(me, amt)
	return simTx{signer: s, label: "token burn", predict: predictionOf(ok),
		msg: &tokentypes.MsgBurn{Sender: me.String(), Denom: t.denom, Amount: amt}}
}

func (c *simChain) opFreeze(s int) simTx {
	me := c.users[s].addr
	t := c.tokenFor(s, func(t *simToken) bool { return t.creator.Equals(me) && t.canFreeze })
	if t == nil {
		return c.opCreate(s)
	}
	account := c.users[c.rng.Intn(simUserCount)].addr
	frozen := c.rng.Intn(2) == 0
	ok := t.predictSetFrozen(me, account, frozen)
	return simTx{signer: s, label: "token freeze", predict: predictionOf(ok),
		msg: &tokentypes.MsgSetFrozen{Sender: me.String(), Denom: t.denom, Account: account.String(), Frozen: frozen}}
}

func (c *simChain) opPause(s int) simTx {
	me := c.users[s].addr
	t := c.tokenFor(s, func(t *simToken) bool { return t.creator.Equals(me) && t.canPause })
	if t == nil {
		return c.opCreate(s)
	}
	paused := c.rng.Intn(2) == 0
	ok := t.predictSetPaused(me, paused)
	return simTx{signer: s, label: "token pause", predict: predictionOf(ok),
		msg: &tokentypes.MsgSetPaused{Sender: me.String(), Denom: t.denom, Paused: paused}}
}
