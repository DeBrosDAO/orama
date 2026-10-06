//go:build e2e_fleet

package chainassets

import (
	"fmt"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
)

// x/token genesis defaults (docs/CHAIN.md "x/token"; x/token/types).
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
		"non_transferable": false, "pause": true, "transfer_hook": false})
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
// docs/CHAIN.md). A zero amount and from == to are refused first.
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

// TestToken_bankSendBypassIsOnlyForFactoryDenoms: x/bank MsgSend does not run
// x/token's checks (docs/CHAIN.md: "x/bank MsgSend does not run these
// checks"), and the norama send restriction does not apply to a factory
// denom: a user-to-user MsgSend of a factory denom is refused only for the
// missing balance, not as a public norama payment.
func TestToken_bankSendBypassIsOnlyForFactoryDenoms(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := c.FundedValidator(t, 0, chain.Orama(1))
	other := c.Validator(t, c.Node(t, 1)).Address
	denom := "factory/" + k.Address + "/e2enever"
	send := chain.NewMsg("/cosmos.bank.v1beta1.MsgSend", map[string]any{"from_address": k.Address, "to_address": other,
		"amount": []any{map[string]any{"denom": denom, "amount": "1"}}})
	r := c.Submit(t, k, chain.TxOptions{}, send)
	chain.RequireRefused(t, "send of a factory denom", r, "insufficient funds")
	if strings.Contains(r.Log, "public user-to-user norama transfer is refused") {
		t.Errorf("the norama restriction refused a factory denom: %s", r.Log)
	}
}
