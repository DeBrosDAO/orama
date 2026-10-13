//go:build e2e_fleet

package chainassets

import (
	"fmt"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
)

// x/token genesis defaults (docs/whitepaper/technical-reference/vol2/39-chain-architecture.md "x/token"; x/token/types).
const (
	creationFeeOrama = 10
	depositPerByte   = 68359
	maxNameLen       = 64
	maxSymbolLen     = 16
	maxDescLen       = 256
	maxFeeBps        = 10_000
)

func tokenMsg(typ string, fields map[string]any) chain.Msg {
	return chain.NewMsg("/orama.token.v1."+typ, fields)
}

func createTokenMsg(creator, sub, name, symbol, desc string, fee uint32) chain.Msg {
	return tokenMsg("MsgCreateToken", map[string]any{"creator": creator, "subdenom": sub, "name": name, "symbol": symbol,
		"description": desc, "mint": true, "freeze": true, "permanent_delegate": "", "transfer_fee_bps": fee,
		"non_transferable": false, "pause": true, "transfer_hook": ""})
}

// TestToken_createNeedsBankBalance: a well-formed MsgCreateToken needs the
// creation fee (10 ORAMA, burned) plus the metadata deposit (68359 norama per
// byte of subdenom, name, symbol and description) as SPENDABLE BANK balance
// (x/token/keeper/create.go), which no run account holds (funds.go), so it is
// refused with the exact amount; every later token message is therefore
// blocked on the run chain.
func TestToken_createNeedsBankBalance(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := c.FundedValidator(t, 0, chain.Orama(1))
	sub, name, symbol, desc := "e2etoken", "E2E Token", "EET", "fleet e2e"
	bytes := int64(len(sub) + len(name) + len(symbol) + len(desc))
	need := chain.Orama(creationFeeOrama).Add(chain.NewInt(bytes * depositPerByte))
	r := c.Submit(t, k, chain.TxOptions{}, createTokenMsg(k.Address, sub, name, symbol, desc, 0))
	denom := "factory/" + k.Address + "/" + sub
	chain.RequireRefused(t, "create without a bank balance", r, "insufficient norama to create "+denom, "need "+need.String())
	if out := c.QueryFails(t, k.Node, "token", "token", denom); !strings.Contains(out, "does not exist") && !chain.NotFound(out) {
		t.Errorf("token %s exists after a refused create: %s", denom, out)
	}
	var p struct {
		Params struct {
			CreationFee    chain.Int `json:"creation_fee"`
			DepositPerByte chain.Int `json:"deposit_per_byte"`
		} `json:"params"`
	}
	c.Query(t, k.Node, &p, "token", "params")
	if p.Params.CreationFee.Cmp(chain.Orama(creationFeeOrama)) != 0 || p.Params.DepositPerByte.Int64() != depositPerByte {
		t.Errorf("token params %+v, want the documented 10 ORAMA fee and 68359 norama per byte", p.Params)
	}
	c.RequireInvariants(t, "a refused token create")
}

// TestToken_createSucceedsWithFaucetFunds: the success path of MsgCreateToken
// that TestToken_createNeedsBankBalance could not reach before the faucet. A
// fresh key funded with 30 ORAMA through `orama chain faucet` pays the
// creation fee (10 ORAMA, burned) and the metadata deposit as BANK balance,
// the token exists afterwards, and the key's balance fell by at least the
// creation fee.
func TestToken_createSucceedsWithFaucetFunds(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	funded := chain.Orama(30)
	k := c.NewFundedKey(t, c.Node(t, 1), "e2e-token-funded", funded)
	sub, name, symbol, desc := chain.UniqueID(t, "e2e"), "E2E Funded Token", "EFT", "fleet e2e, faucet funded"
	chain.RequireOK(t, "create with a funded key", c.Submit(t, k, chain.TxOptions{}, createTokenMsg(k.Address, sub, name, symbol, desc, 0)))
	denom := "factory/" + k.Address + "/" + sub
	if out := c.QueryOut(t, k.Node, "token", "token", denom); out.Exit != 0 {
		t.Errorf("token %s is not readable after a successful create: %s", denom, out.Stderr)
	}
	left := c.Bank(t, k.Node, k.Address)
	if max := funded.Sub(chain.Orama(creationFeeOrama)); left.Cmp(max) > 0 {
		t.Errorf("balance %s norama after the create, want at most %s (the creation fee is burned)", left.String(), max.String())
	}
	c.RequireInvariants(t, "a funded token create")
}

// TestToken_createRefusesATransferHookThatIsNoContract: a token's transfer hook names a contract
// (docs/whitepaper/technical-reference/vol2/40-economics.md "x/token"): an account that is no contract is refused at creation, and so is a
// string that is no address.
func TestToken_createRefusesATransferHookThatIsNoContract(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := c.NewFundedKey(t, c.Node(t, 1), "e2e-token-hook", chain.Orama(30))
	for hook, want := range map[string]string{
		k.Address:            "there is no contract at",
		"orama1notanaddress": "invalid transfer hook contract",
	} {
		m := createTokenMsg(k.Address, chain.UniqueID(t, "e2e"), "E2E Hook", "EHK", "fleet e2e", 0)
		m["transfer_hook"] = hook
		chain.RequireRefused(t, "hook "+hook, c.Submit(t, k, chain.TxOptions{}, m), want)
	}
	c.RequireInvariants(t, "refused hook creations")
}

// TestToken_createShapeRefusals: subdenom, name, symbol, description and
// transfer fee bounds are checked before any fee (boundary values).
func TestToken_createShapeRefusals(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := c.FundedValidator(t, 0, chain.Orama(1))
	cases := map[string]struct {
		m    chain.Msg
		want string
	}{
		"subdenom with a capital":  {createTokenMsg(k.Address, "E2e", "n", "S", "", 0), "subdenom"},
		"one-letter subdenom":      {createTokenMsg(k.Address, "e", "n", "S", "", 0), "subdenom"},
		"45-char subdenom":         {createTokenMsg(k.Address, "e"+strings.Repeat("a", 44), "n", "S", "", 0), "subdenom"},
		"empty name":               {createTokenMsg(k.Address, "e2eok", "", "S", "", 0), "name length must be 1-64"},
		"65-byte name":             {createTokenMsg(k.Address, "e2eok", strings.Repeat("n", maxNameLen+1), "S", "", 0), "name length must be 1-64"},
		"17-byte symbol":           {createTokenMsg(k.Address, "e2eok", "n", strings.Repeat("S", maxSymbolLen+1), "", 0), "symbol length must be 1-16"},
		"symbol with a dash":       {createTokenMsg(k.Address, "e2eok", "n", "S-1", "", 0), "must be alphanumeric"},
		"257-byte description":     {createTokenMsg(k.Address, "e2eok", "n", "S", strings.Repeat("d", maxDescLen+1), 0), "description length must be 0-256"},
		"control byte in the name": {createTokenMsg(k.Address, "e2eok", "n\x01", "S", "", 0), "contains a non-printable byte"},
		"fee over 100%":            {createTokenMsg(k.Address, "e2eok", "n", "S", "", maxFeeBps+1), fmt.Sprintf("transfer_fee_bps %d exceeds %d", maxFeeBps+1, maxFeeBps)},
	}
	for name, tc := range cases {
		chain.RequireRefused(t, name, c.Submit(t, k, chain.TxOptions{}, tc.m), tc.want)
	}
}

// TestToken_messagesNeedAnExistingToken: every other x/token message names a
// token; on a denom that was never created each is refused as not existing
// (after its stateless checks), including norama itself: x/token cannot mint,
// freeze or pause the chain's own denom (only x/emission mints norama,
// docs/whitepaper/technical-reference/vol2/40-economics.md). A zero amount and from == to are refused first.
func TestToken_messagesNeedAnExistingToken(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := c.FundedValidator(t, 0, chain.Orama(1))
	other := c.Validator(t, c.Node(t, 1)).Address
	for _, denom := range []string{"factory/" + k.Address + "/e2enever", chain.Denom} {
		msgs := map[string]chain.Msg{
			"mint":           tokenMsg("MsgMint", map[string]any{"sender": k.Address, "denom": denom, "recipient": k.Address, "amount": "1"}),
			"burn":           tokenMsg("MsgBurn", map[string]any{"sender": k.Address, "denom": denom, "amount": "1"}),
			"transfer":       tokenMsg("MsgTransfer", map[string]any{"sender": k.Address, "from": k.Address, "to": other, "denom": denom, "amount": "1"}),
			"freeze":         tokenMsg("MsgSetFrozen", map[string]any{"sender": k.Address, "denom": denom, "account": other, "frozen": true}),
			"pause":          tokenMsg("MsgSetPaused", map[string]any{"sender": k.Address, "denom": denom, "paused": true}),
			"renounce":       tokenMsg("MsgRenounce", map[string]any{"sender": k.Address, "denom": denom, "extension": "EXTENSION_MINT"}),
			"set shieldable": tokenMsg("MsgSetShieldable", map[string]any{"sender": k.Address, "denom": denom}),
			"delete":         tokenMsg("MsgDeleteToken", map[string]any{"sender": k.Address, "denom": denom}),
		}
		for name, m := range msgs {
			chain.RequireRefused(t, name+" "+denom, c.Submit(t, k, chain.TxOptions{}, m), "token "+denom+" does not exist")
		}
	}
	zero := tokenMsg("MsgMint", map[string]any{"sender": k.Address, "denom": chain.Denom, "recipient": k.Address, "amount": "0"})
	chain.RequireRefused(t, "mint zero", c.Submit(t, k, chain.TxOptions{}, zero), "mint amount")
	self := tokenMsg("MsgTransfer", map[string]any{"sender": k.Address, "from": k.Address, "to": k.Address, "denom": chain.Denom, "amount": "1"})
	chain.RequireRefused(t, "transfer to self", c.Submit(t, k, chain.TxOptions{}, self), "transfer from and to must differ")
	if out := c.QueryFails(t, k.Node, "token", "frozen", chain.Denom, other); !chain.NotFound(out) {
		t.Errorf("frozen query of norama: %s", out)
	}
	c.RequireInvariants(t, "refused token messages")
}

// TestToken_bankSendOfUnknownFactoryDenomIsRefusedForTheBalance: x/token's bank
// restriction governs only tokens that exist: a user-to-user MsgSend of a
// factory denom nobody created is refused only for the missing balance.
func TestToken_bankSendOfUnknownFactoryDenomIsRefusedForTheBalance(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := c.FundedValidator(t, 0, chain.Orama(1))
	other := c.Validator(t, c.Node(t, 1)).Address
	denom := "factory/" + k.Address + "/e2enever"
	send := chain.NewMsg("/cosmos.bank.v1beta1.MsgSend", map[string]any{"from_address": k.Address, "to_address": other,
		"amount": []any{map[string]any{"denom": denom, "amount": "1"}}})
	r := c.Submit(t, k, chain.TxOptions{}, send)
	chain.RequireRefused(t, "send of a factory denom", r, "insufficient funds")
}

// TestToken_bankSendHoldsTokenPowers: a token's pause holds on a plain x/bank
// MsgSend too (docs/whitepaper/technical-reference/vol2/40-economics.md "x/token": the bank send restriction), not only on
// x/token MsgTransfer. A faucet-funded key creates a token with the pause
// power, mints to itself, pauses it, and the bank send is refused; after the
// unpause the same send goes through.
func TestToken_bankSendHoldsTokenPowers(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := c.NewFundedKey(t, c.Node(t, 1), "e2e-token-bank-send", chain.Orama(30))
	other := c.Validator(t, c.Node(t, 0)).Address
	sub := chain.UniqueID(t, "e2e")
	denom := "factory/" + k.Address + "/" + sub
	chain.RequireOK(t, "create", c.Submit(t, k, chain.TxOptions{}, createTokenMsg(k.Address, sub, "E2E Bank Send", "EBS", "fleet e2e", 0)))
	chain.RequireOK(t, "mint", c.Submit(t, k, chain.TxOptions{}, tokenMsg("MsgMint", map[string]any{"sender": k.Address, "denom": denom, "recipient": k.Address, "amount": "10"})))
	send := chain.NewMsg("/cosmos.bank.v1beta1.MsgSend", map[string]any{"from_address": k.Address, "to_address": other,
		"amount": []any{map[string]any{"denom": denom, "amount": "1"}}})
	pause := func(paused bool) chain.Msg {
		return tokenMsg("MsgSetPaused", map[string]any{"sender": k.Address, "denom": denom, "paused": paused})
	}
	chain.RequireOK(t, "pause", c.Submit(t, k, chain.TxOptions{}, pause(true)))
	chain.RequireRefused(t, "bank send of a paused token", c.Submit(t, k, chain.TxOptions{}, send), "is paused")
	chain.RequireOK(t, "unpause", c.Submit(t, k, chain.TxOptions{}, pause(false)))
	chain.RequireOK(t, "bank send after the unpause", c.Submit(t, k, chain.TxOptions{}, send))
	c.RequireInvariants(t, "a bank send of a factory token")
}
