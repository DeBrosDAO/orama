package gen

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/shielded"
	shieldedtypes "github.com/DeBrosOfficial/network/chain/x/shielded/types"
)

func shieldedEnv(t *testing.T) (*shieldedType, []string) {
	t.Helper()
	a := addrs(t, 3)
	return &shieldedType{
		name: "shielded-transfer-2", kind: shielded.EntryTransfer, actions: 2, actionGas: 250_000, accounts: a,
		entries: []shielded.Entry{
			{ID: "t0", Kind: shielded.EntryTransfer, Actions: 2, Account: shielded.Signerless, Bundle: "0102"},
			{ID: "t1", Kind: shielded.EntryTransfer, Actions: 2, Account: shielded.Signerless, Bundle: "0304"},
		},
	}, a
}

func TestShieldedTransfer_isSignerlessWithExactGas(t *testing.T) {
	g, _ := shieldedEnv(t)
	p, err := g.Plan(1)
	if err != nil {
		t.Fatal(err)
	}
	msg, ok := p.Msg.(*shieldedtypes.MsgShieldedTransfer)
	if !ok || p.Account != Signerless || p.Gas != 500_000 || !bytes.Equal(msg.Bundle, []byte{3, 4}) {
		t.Fatalf("planned %+v", p)
	}
	if msg.Signer != shieldedtypes.SignerlessAddress().String() {
		t.Fatalf("signer %s", msg.Signer)
	}
}

func TestShieldedType_consumedIsOnlyWhatWasPlanned(t *testing.T) {
	g, _ := shieldedEnv(t)
	if len(g.Consumed()) != 0 {
		t.Fatal("nothing planned yet, nothing consumed")
	}
	if _, err := g.Plan(0); err != nil {
		t.Fatal(err)
	}
	if got := g.Consumed(); len(got) != 1 || got[0] != "t0" {
		t.Fatalf("consumed %v", got)
	}
	if _, err := g.Plan(1); err != nil {
		t.Fatal(err)
	}
	if got := g.Consumed(); len(got) != 2 {
		t.Fatalf("consumed %v", got)
	}
}

func TestShieldedType_signedKindsUseTheirBenchAccount(t *testing.T) {
	g, a := shieldedEnv(t)
	g.entries = []shielded.Entry{
		{ID: "s0", Kind: shielded.EntryShield, Actions: 2, Account: 2, Bundle: "aa"},
		{ID: "u0", Kind: shielded.EntryUnshield, Actions: 2, Account: 1, Bundle: "bb"},
		{ID: "bad", Kind: shielded.EntryShield, Actions: 2, Account: 9, Bundle: "cc"},
		{ID: "nothex", Kind: shielded.EntryShield, Actions: 2, Account: 0, Bundle: "zz"},
	}
	p, err := g.Plan(0)
	if err != nil || p.Account != 2 || p.Msg.(*shieldedtypes.MsgShield).Signer != a[2] {
		t.Fatalf("shield: %+v, %v", p, err)
	}
	p, err = g.Plan(1)
	if err != nil {
		t.Fatal(err)
	}
	un := p.Msg.(*shieldedtypes.MsgUnshield)
	if p.Account != 1 || un.Signer != a[1] || un.Target != shieldedtypes.UnshieldTargetFeeTopup {
		t.Fatalf("unshield: %+v", un)
	}
	if err := un.ValidateBasic(); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Plan(2); err == nil {
		t.Error("an entry for a bench account that does not exist was planned")
	}
	if _, err := g.Plan(3); err == nil {
		t.Error("an entry that is not hex was planned")
	}
	if _, err := g.Plan(4); err == nil {
		t.Error("a plan past the end of the cache was allowed")
	}
}

func TestShieldedType_skipsWithoutACacheOrWithAnEmptyOne(t *testing.T) {
	g, err := New("shield")
	if err != nil {
		t.Fatal(err)
	}
	if _, skipped := SkipReason(g.Prepare(context.Background(), &Env{}, 5)); !skipped {
		t.Fatal("no cache must skip, not fail")
	}
	empty := &shielded.Cache{}
	if reason, skipped := SkipReason(g.Prepare(context.Background(), &Env{Shielded: empty}, 5)); !skipped || !strings.Contains(reason, "no unused shield bundle") {
		t.Fatalf("empty cache: %q, %v", reason, skipped)
	}
}

func TestTransferActions(t *testing.T) {
	for _, n := range TransferActionCounts {
		got, ok := TransferActions(fmt.Sprintf("shielded-transfer-%d", n))
		if !ok || got != n {
			t.Errorf("shielded-transfer-%d: %d, %v", n, got, ok)
		}
	}
	for _, bad := range []string{"shielded-transfer-3", "shielded-transfer-", "shield", "bank-send"} {
		if _, ok := TransferActions(bad); ok {
			t.Errorf("%q read as a transfer type", bad)
		}
	}
}
