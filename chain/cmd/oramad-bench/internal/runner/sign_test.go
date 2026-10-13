package runner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/bech32"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"

	"github.com/DeBrosOfficial/network/chain/client/node"
	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/account"
	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/gen"
	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/ops"
)

const testChainID = "orama-localnet-1"

// offlineSigner builds a signer over a client that is never used to reach a node: signing needs only
// the codec.
func offlineSigner(t *testing.T, n int) (*signer, []string) {
	t.Helper()
	client, err := node.DialWith("tcp://127.0.0.1:1", registrations()...)
	if err != nil {
		t.Fatal(err)
	}
	chain, err := ops.New(client, testChainID, ops.DefaultFeeMultiple, ops.DefaultGasAdjustPercent)
	if err != nil {
		t.Fatal(err)
	}
	accts, err := account.DeriveN("sign-test", n)
	if err != nil {
		t.Fatal(err)
	}
	s := newSigner(chain, accts)
	addrs := make([]string, n)
	for i, a := range accts {
		addrs[i] = a.Address
		s.seq.Set(a.Address, uint64(100*i))
		s.numbers[a.Address] = uint64(7 + i)
	}
	return s, addrs
}

func bankGen(t *testing.T, s *signer) gen.Generator {
	t.Helper()
	g, err := gen.New(gen.BankSend)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Prepare(context.Background(), &gen.Env{Chain: s.chain, Accounts: s.accounts}, 0); err != nil {
		t.Fatal(err)
	}
	return g
}

func TestPresign_sequencesAreConsecutivePerAccountAndSignaturesVerify(t *testing.T) {
	s, addrs := offlineSigner(t, 4)
	g := bankGen(t, s)
	jobs, err := s.presign(g, 0, 12, 90_000, math.NewInt(32))
	if err != nil {
		t.Fatal(err)
	}
	next := map[string]uint64{}
	for i, a := range addrs {
		next[a] = uint64(100 * i)
	}
	for i, j := range jobs {
		if j.Seq != next[j.Account] {
			t.Fatalf("job %d of %s has sequence %d, want %d: sequences must be consecutive per account in plan order", i, j.Account, j.Seq, next[j.Account])
		}
		next[j.Account]++
		sum := sha256.Sum256(j.Raw)
		if j.Hash != strings.ToUpper(hex.EncodeToString(sum[:])) {
			t.Fatalf("job %d hash %s is not the SHA-256 of its bytes", i, j.Hash)
		}
		decoded, err := s.chain.Client.TxBuilder().Decode(j.Raw, testChainID, s.numbers[j.Account])
		if err != nil {
			t.Fatalf("job %d does not verify as signed by %s: %v", i, j.Account, err)
		}
		_, got, err1 := bech32.DecodeAndConvert(decoded.Signer)
		_, want, err2 := bech32.DecodeAndConvert(j.Account)
		if err1 != nil || err2 != nil || !bytes.Equal(got, want) {
			t.Fatalf("job %d signed by %s, expected %s", i, decoded.Signer, j.Account)
		}
	}
	for _, a := range addrs {
		if got, _ := s.seq.Peek(a); got != next[a] {
			t.Fatalf("the sequencer is at %d for %s, signing ended at %d", got, a, next[a])
		}
	}
}

func TestPresign_aSecondBatchContinuesWhereTheFirstStopped(t *testing.T) {
	s, _ := offlineSigner(t, 2)
	g := bankGen(t, s)
	first, err := s.presign(g, 0, 4, 90_000, math.NewInt(1))
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.presign(g, 4, 4, 90_000, math.NewInt(1))
	if err != nil {
		t.Fatal(err)
	}
	last := map[string]uint64{}
	for _, j := range first {
		last[j.Account] = j.Seq
	}
	for _, j := range second {
		if j.Seq != last[j.Account]+1 {
			t.Fatalf("%s: second batch starts at %d after %d", j.Account, j.Seq, last[j.Account])
		}
		last[j.Account] = j.Seq
	}
}

func TestPresign_errors(t *testing.T) {
	s, addrs := offlineSigner(t, 2)
	g := bankGen(t, s)
	// An account the sequencer was never loaded for cannot be signed for.
	fresh := newSigner(s.chain, s.accounts)
	if _, err := fresh.presign(g, 0, 1, 90_000, math.NewInt(1)); err == nil || !strings.Contains(err.Error(), "no sequence") {
		t.Fatalf("got %v", err)
	}
	// A plan for an account that does not exist.
	bad := fixedGen{p: gen.Planned{Account: 9, Msg: banktypes.NewMsgSend(sdk.MustAccAddressFromBech32(addrs[0]), sdk.MustAccAddressFromBech32(addrs[1]), sdk.NewCoins(sdk.NewInt64Coin("norama", 1)))}}
	if _, err := s.presign(bad, 0, 1, 90_000, math.NewInt(1)); err == nil {
		t.Fatal("a plan for account 9 of 2 was signed")
	}
	if jobs, err := s.presign(g, 0, 0, 90_000, math.NewInt(1)); err != nil || len(jobs) != 0 {
		t.Fatalf("zero transactions: %d, %v", len(jobs), err)
	}
}

func TestPresign_signerlessTransactionsCarryNoSignatureAndTheirOwnGas(t *testing.T) {
	s, addrs := offlineSigner(t, 2)
	msg := banktypes.NewMsgSend(sdk.MustAccAddressFromBech32(addrs[0]), sdk.MustAccAddressFromBech32(addrs[1]), sdk.NewCoins(sdk.NewInt64Coin("norama", 1)))
	g := fixedGen{p: gen.Planned{Account: gen.Signerless, Msg: msg, Gas: 500_000}}
	jobs, err := s.presign(g, 0, 3, 90_000, math.NewInt(1))
	if err != nil {
		t.Fatal(err)
	}
	if jobs[0].Account != "" || jobs[0].Hash == "" {
		t.Fatalf("job %+v", jobs[0])
	}
	if jobs[0].Hash != jobs[1].Hash || jobs[0].Shard == jobs[1].Shard {
		t.Fatal("identical signerless transactions share a hash; their shards must still differ so they spread over workers")
	}
	tx, err := s.chain.Client.TxConfig().TxDecoder()(jobs[0].Raw)
	if err != nil {
		t.Fatal(err)
	}
	if gas := tx.(interface{ GetGas() uint64 }).GetGas(); gas != 500_000 {
		t.Fatalf("gas %d", gas)
	}
	for _, a := range addrs {
		if got, _ := s.seq.Peek(a); got%100 != 0 {
			t.Fatalf("a signerless transaction consumed a sequence of %s", a)
		}
	}
}

func TestReload_readsNumbersAndSequences(t *testing.T) {
	s, _ := offlineSigner(t, 2)
	// With no node behind the client, a reload must fail naming the account instead of leaving a stale sequence.
	if err := s.reload(context.Background()); err == nil {
		t.Fatal("reload against a dead node reported success")
	}
}

// fixedGen plans the same message for every k.
type fixedGen struct{ p gen.Planned }

func (fixedGen) Name() string                                 { return "fixed" }
func (fixedGen) Describe() string                             { return "fixed" }
func (fixedGen) Prepare(context.Context, *gen.Env, int) error { return nil }
func (fixedGen) Available() int                               { return gen.Unlimited }
func (f fixedGen) Plan(int) (gen.Planned, error)              { return f.p, nil }
