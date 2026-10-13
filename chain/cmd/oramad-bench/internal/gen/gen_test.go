package gen

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/account"
	tokentypes "github.com/DeBrosOfficial/network/chain/x/token/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
)

func addrs(t *testing.T, n int) []string {
	t.Helper()
	accts, err := account.DeriveN("gen-test", n)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, n)
	for i, a := range accts {
		out[i] = a.Address
	}
	return out
}

func TestNamesAndNew(t *testing.T) {
	names := Names()
	for _, want := range []string{BankSend, TokenTransfer, WasmCall, StorageDeal, RegisterNode, CnftMint, MarketList, Shield, Unshield, "shielded-transfer-2", "shielded-transfer-4", "shielded-transfer-8", "shielded-transfer-16"} {
		found := false
		for _, n := range names {
			found = found || n == want
		}
		if !found {
			t.Errorf("transaction type %s is not registered", want)
		}
		g, err := New(want)
		if err != nil || g.Name() != want || g.Describe() == "" {
			t.Errorf("New(%q) = %v, %v", want, g, err)
		}
	}
	if _, err := New("no-such-type"); err == nil || !strings.Contains(err.Error(), "known types") {
		t.Fatalf("an unknown type must name the known ones: %v", err)
	}
}

func TestSkipError(t *testing.T) {
	err := fmt.Errorf("wrapped: %w", Skipf("no %s", "wasm"))
	reason, ok := SkipReason(err)
	if !ok || reason != "no wasm" {
		t.Fatalf("got %q, %v", reason, ok)
	}
	if _, ok := SkipReason(errors.New("boom")); ok {
		t.Fatal("a plain error read as a skip")
	}
}

func TestPair_neverSendsToSelfAndWraps(t *testing.T) {
	for n := 2; n <= 5; n++ {
		for k := 0; k < 3*n; k++ {
			from, to := pair(k, n)
			if from == to || from != k%n || to != (k+1)%n {
				t.Fatalf("n=%d k=%d: %d -> %d", n, k, from, to)
			}
		}
	}
}

func TestBankSend_plansAValidSendBetweenNeighbours(t *testing.T) {
	g := &bankSend{accounts: addrs(t, 4)}
	for k := 0; k < 8; k++ {
		p, err := g.Plan(k)
		if err != nil {
			t.Fatal(err)
		}
		msg := p.Msg.(*banktypes.MsgSend)
		if msg.FromAddress != g.accounts[k%4] || msg.ToAddress != g.accounts[(k+1)%4] || !msg.Amount.IsValid() || p.Account != k%4 {
			t.Fatalf("k=%d: %+v signed by account %d", k, msg, p.Account)
		}
	}
	if err := (&bankSend{}).Prepare(context.Background(), &Env{Accounts: nil}, 0); err == nil {
		t.Fatal("bank-send prepared with no accounts")
	}
}

func TestTokenTransfer_plansTransfersOfTheRunsToken(t *testing.T) {
	a := addrs(t, 3)
	g := &tokenTransfer{denom: tokentypes.Denom(a[0], "benchx"), accounts: a}
	p, err := g.Plan(4)
	if err != nil {
		t.Fatal(err)
	}
	msg := p.Msg.(*tokentypes.MsgTransfer)
	if msg.Sender != a[1] || msg.From != a[1] || msg.To != a[2] || msg.Denom != g.denom || !msg.Amount.IsPositive() {
		t.Fatalf("message %+v", msg)
	}
}
